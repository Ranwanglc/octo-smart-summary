package handler

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/api/ws"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/middleware"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/service"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/timezone"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var triggerClient = &http.Client{Timeout: 5 * time.Second}

// PersonalHandler handles P2 by-person endpoints.
type PersonalHandler struct {
	db               *gorm.DB
	workerTriggerURL string
	hub              *ws.Hub
}

// NewPersonalHandler creates a new PersonalHandler.
func NewPersonalHandler(db *gorm.DB, workerTriggerURL string, hub *ws.Hub) *PersonalHandler {
	return &PersonalHandler{db: db, workerTriggerURL: workerTriggerURL, hub: hub}
}

func (h *PersonalHandler) parseTaskID(c *gin.Context) (int64, bool) {
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "invalid task id"})
		return 0, false
	}
	return taskID, true
}

// Accept handles POST /api/v1/summaries/:id/accept
func (h *PersonalHandler) Accept(c *gin.Context) {
	userID := middleware.GetUserID(c)
	taskID, valid := h.parseTaskID(c)
	if !valid {
		return
	}

	var participant model.SummaryParticipant
	if err := h.db.Where("task_id = ? AND user_id = ?", taskID, userID).First(&participant).Error; err != nil {
		c.JSON(http.StatusNotFound, apiResponse{Code: 40008, Message: "你不是该任务的参与者"})
		return
	}

	// Idempotent: already accepted or beyond → 200
	if participant.Status == model.ParticipantAccepted ||
		participant.Status == model.ParticipantProcessing ||
		participant.Status == model.ParticipantCompleted ||
		participant.Status == model.ParticipantSubmitted {
		ok(c, gin.H{"status": model.ParticipantStatusLabel(participant.Status)})
		return
	}

	// Declined cannot be undone
	if participant.Status == model.ParticipantDeclined {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40005, Message: "已拒绝，不能反悔"})
		return
	}

	now := timezone.Now()

	// 🔴 Unique-key 500 fix: the AUTO scheduled dispatch path may pre-create a
	// summary_personal_result row (uk_task_participant(task_id, participant_ref_id))
	// while the participant itself is still Pending. The status-only idempotency
	// guard above does not catch that case, so the old UNCONDITIONAL tx.Create(&pr)
	// violated the unique key and returned 500 "Duplicate entry" -- the user saw
	// their Accept fail / appear as a reject. Make the create idempotent: upsert with
	// DoNothing on conflict, then read back the surviving row and reuse it. This is
	// the same pattern bootstrapCreatorParticipant uses in the worker (zero DB/schema
	// change, pure application-level idempotency).
	var prCompleted bool
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// Update participant to accepted
		if err := tx.Model(&participant).Updates(map[string]interface{}{
			"status":       model.ParticipantAccepted,
			"confirmed_at": now,
		}).Error; err != nil {
			return err
		}

		// Idempotent create of the personal result. On conflict with an existing
		// (task_id, participant_ref_id) row, do nothing and reuse the existing row.
		pr := model.PersonalResult{
			TaskID:           taskID,
			ParticipantRefID: participant.ID,
			UserID:           userID,
			Content:          "",
			WorkerStatus:     model.PersonalStatusPending,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "task_id"}, {Name: "participant_ref_id"}},
			DoNothing: true,
		}).Create(&pr)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// A personal_result already exists (e.g. pre-created by AUTO dispatch).
			// Reuse it instead of inserting a duplicate.
			if err := tx.Where("task_id = ? AND participant_ref_id = ?", taskID, participant.ID).
				First(&pr).Error; err != nil {
				return err
			}
			// If the existing row is in a terminal state (Completed/Submitted), don't
			// reset it -- accept stays idempotent and the worker is not re-triggered
			// over a finished result. Otherwise make sure worker_status is Pending so
			// scheduledAutoDispatchTargets (Accepted && worker_status==Pending) can pick
			// it up for (re)dispatch.
			if pr.WorkerStatus == model.PersonalStatusCompleted || pr.SubmittedAt != nil {
				prCompleted = true
			} else if pr.WorkerStatus != model.PersonalStatusPending {
				if err := tx.Model(&model.PersonalResult{}).Where("id = ?", pr.ID).
					Update("worker_status", model.PersonalStatusPending).Error; err != nil {
					return err
				}
			}
		}

		// Link personal_result_id back to participant
		return tx.Model(&participant).Update("personal_result_id", pr.ID).Error
	})
	if err != nil {
		log.Printf("[personal] accept tx error: %v", err)
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 50000, Message: err.Error()})
		return
	}

	// Trigger personal summary worker (async, non-blocking). Skip when the existing
	// result is already terminal so we never overwrite a finished/submitted summary.
	if !prCompleted {
		go h.triggerWorker(model.WorkerTriggerRequest{
			Type:             "personal_summary",
			TaskID:           taskID,
			ParticipantRefID: participant.ID,
		})
	}

	ok(c, gin.H{"status": "accepted"})
}

// Decline handles POST /api/v1/summaries/:id/decline
func (h *PersonalHandler) Decline(c *gin.Context) {
	userID := middleware.GetUserID(c)
	taskID, valid := h.parseTaskID(c)
	if !valid {
		return
	}

	var participant model.SummaryParticipant
	if err := h.db.Where("task_id = ? AND user_id = ?", taskID, userID).First(&participant).Error; err != nil {
		c.JSON(http.StatusNotFound, apiResponse{Code: 40008, Message: "你不是该任务的参与者"})
		return
	}

	// Only pending participants can decline
	if participant.Status != model.ParticipantPending {
		if participant.Status == model.ParticipantDeclined {
			ok(c, gin.H{"status": "declined"})
			return
		}
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40005, Message: "当前状态不允许拒绝"})
		return
	}

	h.db.Model(&participant).Update("status", model.ParticipantDeclined)
	ok(c, gin.H{"status": "declined"})
}

// Respond handles POST /api/v1/summaries/:id/respond
// Accepts {"action": "accept"} or {"action": "reject"}.
func (h *PersonalHandler) Respond(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "invalid request body"})
		return
	}

	switch req.Action {
	case "accept":
		h.Accept(c)
	case "reject":
		h.Decline(c)
	default:
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "action must be 'accept' or 'reject'"})
	}
}

// GetPersonal handles GET /api/v1/summaries/:id/personal
func (h *PersonalHandler) GetPersonal(c *gin.Context) {
	userID := middleware.GetUserID(c)
	taskID, valid := h.parseTaskID(c)
	if !valid {
		return
	}

	var pr model.PersonalResult
	if err := h.db.Where("task_id = ? AND user_id = ?", taskID, userID).First(&pr).Error; err != nil {
		// Not found → return default
		ok(c, gin.H{
			"worker_status": 0,
			"content":       "",
			"submitted_at":  nil,
			"generated_at":  nil,
			"msg_count":     0,
		})
		return
	}

	result := gin.H{
		"worker_status": pr.WorkerStatus,
		"content":       pr.Content,
		"citations":     pr.GetCitations(),
		"submitted_at":  nil,
		"generated_at":  nil,
		"msg_count":     pr.MsgCount,
	}
	if pr.SubmittedAt != nil {
		result["submitted_at"] = pr.SubmittedAt.Format(time.RFC3339)
	}
	if pr.GeneratedAt != nil {
		result["generated_at"] = pr.GeneratedAt.Format(time.RFC3339)
	}
	ok(c, result)
}

// Submit handles POST /api/v1/summaries/:id/submit
func (h *PersonalHandler) Submit(c *gin.Context) {
	userID := middleware.GetUserID(c)
	taskID, valid := h.parseTaskID(c)
	if !valid {
		return
	}

	var pr model.PersonalResult
	if err := h.db.Where("task_id = ? AND user_id = ?", taskID, userID).First(&pr).Error; err != nil {
		c.JSON(http.StatusNotFound, apiResponse{Code: 40008, Message: "个人总结不存在"})
		return
	}

	if pr.WorkerStatus != model.PersonalStatusCompleted {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40005, Message: "个人总结未完成，无法提交"})
		return
	}

	// Idempotent fast-path: already submitted (avoids a needless write). The
	// authoritative idempotency guard is the conditional UPDATE below; this is just
	// an early exit for the common case.
	if pr.SubmittedAt != nil {
		ok(c, gin.H{"status": "submitted"})
		return
	}

	now := timezone.Now()
	// 🔴 Blocker-2 fix: concurrency-safe submit. The old code did read-then-check
	// (pr.SubmittedAt==nil) then an UNCONDITIONAL Updates -- racing the system
	// back-fill (backfillScheduledSubmittedAt, submit_source=2) it could overwrite a
	// system-written row, flipping submit_source back to 1 with no CAS/transaction.
	// Use a conditional UPDATE ... WHERE submitted_at IS NULL so exactly one writer
	// (manual OR system) ever sets submitted_at; the loser sees RowsAffected==0 and
	// returns the idempotent "already submitted" response WITHOUT rewriting source.
	res := h.db.Model(&model.PersonalResult{}).
		Where("id = ? AND submitted_at IS NULL", pr.ID).
		Updates(map[string]interface{}{
			"submitted_at":  now,
			"submit_source": model.SubmitSourceManual,
		})
	if res.Error != nil {
		log.Printf("[personal] submit conditional update error: %v", res.Error)
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 50000, Message: res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		// Already submitted by a concurrent manual /submit or the system back-fill.
		// Do NOT rewrite submit_source; return the same idempotent "submitted" semantics.
		ok(c, gin.H{"status": "submitted"})
		return
	}

	// Update participant status to submitted
	h.db.Model(&model.SummaryParticipant{}).
		Where("task_id = ? AND user_id = ?", taskID, userID).
		Update("status", model.ParticipantSubmitted)

	// Broadcast MEMBER_SUBMITTED event to all subscribers
	if h.hub != nil {
		h.hub.Broadcast(taskID, gin.H{
			"type": ws.EventMemberSubmitted,
			"payload": gin.H{
				"task_id": taskID,
				"user_id": userID,
			},
		})
	}

	// Trigger meta summary worker (async)
	go h.triggerWorker(model.WorkerTriggerRequest{
		Type:   "meta_summary",
		TaskID: taskID,
	})

	ok(c, gin.H{"status": "submitted"})
}

// GetMembers handles GET /api/v1/summaries/:id/members
func (h *PersonalHandler) GetMembers(c *gin.Context) {
	taskID, valid := h.parseTaskID(c)
	if !valid {
		return
	}

	// Authorization: only the task creator or an explicit participant may read the
	// member list. Source-group membership alone does NOT grant access. This mirrors
	// TaskHandler.authorizeTaskAccess / canAccessTask (codes 4010 / 40008 / 40003).
	userID := middleware.GetUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 4010, Message: "authentication required"})
		return
	}

	var task model.SummaryTask
	if err := h.db.Where("id = ? AND deleted_at IS NULL", taskID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, apiResponse{Code: 40008, Message: "任务不存在"})
		return
	}

	if task.CreatorID != userID {
		var cnt int64
		h.db.Model(&model.SummaryParticipant{}).
			Where("task_id = ? AND user_id = ?", taskID, userID).
			Count(&cnt)
		if cnt == 0 {
			c.JSON(http.StatusForbidden, apiResponse{Code: 40003, Message: "无权访问此任务"})
			return
		}
	}

	var participants []model.SummaryParticipant
	h.db.Where("task_id = ?", taskID).Find(&participants)

	// Batch load personal results for submitted_at
	prMap := make(map[int64]*model.PersonalResult)
	if len(participants) > 0 {
		var prs []model.PersonalResult
		h.db.Where("task_id = ?", taskID).Find(&prs)
		for i := range prs {
			prMap[prs[i].ParticipantRefID] = &prs[i]
		}
	}

	members := make([]gin.H, 0, len(participants))
	for _, p := range participants {
		userName := p.UserName
		if p.UserID != "" {
			if resolved := service.ResolveUserName(p.UserID); resolved != p.UserID {
				userName = resolved
			}
		}
		member := gin.H{
			"user_id":      p.UserID,
			"user_name":    userName,
			"status":       model.ParticipantStatusLabel(p.Status),
			"submitted_at": nil,
		}
		if pr, exists := prMap[p.ID]; exists && pr.SubmittedAt != nil {
			member["submitted_at"] = pr.SubmittedAt.Format(time.RFC3339)
			member["content"] = pr.Content
			member["citations"] = pr.GetCitations()
		}
		members = append(members, member)
	}

	ok(c, gin.H{"members": members})
}

func (h *PersonalHandler) triggerWorker(req model.WorkerTriggerRequest) {
	if h.workerTriggerURL == "" {
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		log.Printf("[personal] marshal trigger: %v", err)
		return
	}
	resp, err := triggerClient.Post(h.workerTriggerURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[personal] trigger worker POST failed: %v", err)
		return
	}
	resp.Body.Close()
}
