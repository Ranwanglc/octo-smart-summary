package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/middleware"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TaskHandler handles summary task endpoints.
type TaskHandler struct {
	db *gorm.DB
}

// NewTaskHandler creates a new TaskHandler.
func NewTaskHandler(db *gorm.DB) *TaskHandler {
	return &TaskHandler{db: db}
}

type apiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, apiResponse{Code: 0, Message: "ok", Data: data})
}

func bizErr(c *gin.Context, err *service.BizError) {
	c.JSON(err.HTTPStatus, apiResponse{Code: err.Code, Message: err.Message})
}

type createSummaryReq struct {
	UID                 string       `json:"uid"`
	Title               string       `json:"title"`
	Topic               string       `json:"topic"`
	SummaryMode         *int         `json:"summary_mode"`
	TimeRange           *timeRange   `json:"time_range"`
	Sources             []sourceReq  `json:"sources"`
	Participants        []participantReq `json:"participants"`
	ConfirmTimeoutHours int          `json:"confirm_timeout_hours"`
}

type timeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type sourceReq struct {
	SourceType int    `json:"source_type"`
	SourceID   string `json:"source_id"`
}

type participantReq struct {
	UserID string `json:"user_id"`
}

// CreateSummary handles POST /api/v1/summaries
func (h *TaskHandler) CreateSummary(c *gin.Context) {
	spaceID := middleware.GetSpaceID(c)
	userID := middleware.GetUserID(c)

	var req createSummaryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: err.Error()})
		return
	}

	if req.ConfirmTimeoutHours <= 0 {
		req.ConfirmTimeoutHours = 24
	}

	effectiveUID := req.UID
	if effectiveUID == "" {
		effectiveUID = userID
	}

	// Validate
	if len(req.Sources) == 0 && req.Topic == "" && req.TimeRange == nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40001, Message: "至少提供 sources、topic 或 time_range 之一"})
		return
	}

	// Infer scope if no sources
	var inferredScope map[string]interface{}
	if len(req.Sources) == 0 {
		topic := req.Topic
		if topic == "" {
			topic = req.Title
		}
		if topic == "" {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 40001, Message: "请提供总结主题或指定信息来源"})
			return
		}
		inferredScope = service.InferScope(topic)
	}

	// Resolve summary_mode
	summaryMode := 1
	if req.SummaryMode != nil {
		summaryMode = *req.SummaryMode
	} else if inferredScope != nil {
		if m, ok := inferredScope["summary_mode"].(int); ok {
			summaryMode = m
		}
	}

	// Resolve time range
	var timeStart, timeEnd time.Time
	if req.TimeRange != nil {
		timeStart = req.TimeRange.Start
		timeEnd = req.TimeRange.End
	} else {
		timeEnd = time.Now().UTC()
		timeStart = timeEnd.Add(-7 * 24 * time.Hour)
	}

	if timeEnd.Sub(timeStart) > 31*24*time.Hour {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40002, Message: "时间范围不能超过31天"})
		return
	}

	// Resolve sources
	var sourceList []sourceReq
	if len(req.Sources) > 0 {
		sourceList = req.Sources
	} else if inferredScope != nil {
		if sources, ok := inferredScope["sources"]; ok {
			if sl, ok := sources.([]struct {
				SourceType int    `json:"source_type"`
				SourceID   string `json:"source_id"`
				SourceName string `json:"source_name"`
			}); ok {
				for _, s := range sl {
					sourceList = append(sourceList, sourceReq{SourceType: s.SourceType, SourceID: s.SourceID})
				}
			}
		}
	}

	if len(sourceList) > 10 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40003, Message: "信息来源不能超过10个"})
		return
	}

	if summaryMode == model.ModeByPerson && len(req.Participants) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40001, Message: "按人模式必须指定参与者"})
		return
	}

	taskNo := service.GenerateTaskNo()
	title := req.Title
	if title == "" {
		title = req.Topic
	}
	if title == "" {
		title = "总结-" + taskNo[len(taskNo)-8:]
	}

	initialStatus := model.StatusPending
	var confirmDeadline *time.Time
	if summaryMode == model.ModeByPerson {
		initialStatus = model.StatusWaitingConfirm
		dl := time.Now().UTC().Add(time.Duration(req.ConfirmTimeoutHours) * time.Hour)
		confirmDeadline = &dl
	}

	task := model.SummaryTask{
		TaskNo:          taskNo,
		SpaceID:         spaceID,
		CreatorID:       effectiveUID,
		Title:           title,
		SummaryMode:     summaryMode,
		TimeRangeStart:  timeStart,
		TimeRangeEnd:    timeEnd,
		Status:          initialStatus,
		TriggerType:     model.TriggerManual,
		ConfirmDeadline: confirmDeadline,
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		for _, s := range sourceList {
			src := model.SummarySource{
				TaskID:     task.ID,
				SourceType: s.SourceType,
				SourceID:   s.SourceID,
				SourceName: service.ResolveSourceName(s.SourceID),
			}
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
		}
		if summaryMode == model.ModeByPerson {
			now := time.Now().UTC()
			creatorP := model.SummaryParticipant{
				TaskID:      task.ID,
				UserID:      effectiveUID,
				UserName:    "用户" + effectiveUID,
				Status:      1,
				ConfirmedAt: &now,
			}
			if err := tx.Create(&creatorP).Error; err != nil {
				return err
			}
			for _, p := range req.Participants {
				if p.UserID == effectiveUID {
					continue
				}
				pp := model.SummaryParticipant{
					TaskID:   task.ID,
					UserID:   p.UserID,
					UserName: "用户" + p.UserID,
				}
				if err := tx.Create(&pp).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 50000, Message: err.Error()})
		return
	}

	result := gin.H{
		"task_id":    task.ID,
		"task_no":    task.TaskNo,
		"status":     task.Status,
		"created_at": task.CreatedAt.Format(time.RFC3339),
	}
	if inferredScope != nil {
		result["inferred"] = true
		result["inferred_sources"] = inferredScope["sources"]
	}
	ok(c, result)
}

// ListSummaries handles GET /api/v1/summaries
func (h *TaskHandler) ListSummaries(c *gin.Context) {
	spaceID := middleware.GetSpaceID(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := h.db.Model(&model.SummaryTask{}).
		Where("space_id = ? AND deleted_at IS NULL", spaceID)

	if s := c.Query("status"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			query = query.Where("status = ?", v)
		}
	}
	if s := c.Query("summary_mode"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			query = query.Where("summary_mode = ?", v)
		}
	}
	if s := c.Query("trigger_type"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			query = query.Where("trigger_type = ?", v)
		}
	}
	if s := c.Query("keyword"); s != "" {
		query = query.Where("title LIKE ?", "%"+s+"%")
	}
	if s := c.Query("created_after"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if s := c.Query("created_before"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			query = query.Where("created_at <= ?", t)
		}
	}

	var total int64
	query.Count(&total)

	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	orderClause := sortBy + " " + sortOrder
	if sortBy != "created_at" && sortBy != "updated_at" {
		orderClause = "created_at desc"
	}

	var tasks []model.SummaryTask
	query.Order(orderClause).Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks)

	items := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		var sources []model.SummarySource
		h.db.Where("task_id = ?", t.ID).Find(&sources)

		srcList := make([]gin.H, 0, len(sources))
		for _, s := range sources {
			srcList = append(srcList, gin.H{
				"source_type": s.SourceType,
				"source_id":   s.SourceID,
				"source_name": s.SourceName,
			})
		}

		var latestResult model.SummaryResult
		h.db.Where("task_id = ?", t.ID).Order("version DESC").Limit(1).Find(&latestResult)

		totalMsgCount := 0
		var completedAt *string
		if latestResult.ID > 0 {
			totalMsgCount = latestResult.TotalMsgCount
			s := latestResult.GeneratedAt.Format(time.RFC3339)
			completedAt = &s
		}

		items = append(items, gin.H{
			"task_id":          t.ID,
			"task_no":          t.TaskNo,
			"title":            t.Title,
			"summary_mode":     t.SummaryMode,
			"status":           t.Status,
			"trigger_type":     t.TriggerType,
			"time_range_start": t.TimeRangeStart.Format(time.RFC3339),
			"time_range_end":   t.TimeRangeEnd.Format(time.RFC3339),
			"sources":          srcList,
			"total_msg_count":  totalMsgCount,
			"created_at":       t.CreatedAt.Format(time.RFC3339),
			"completed_at":     completedAt,
		})
	}

	ok(c, gin.H{"total": total, "items": items})
}

// GetSummary handles GET /api/v1/summaries/:id
func (h *TaskHandler) GetSummary(c *gin.Context) {
	spaceID := middleware.GetSpaceID(c)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "invalid task id"})
		return
	}

	var task model.SummaryTask
	if err := h.db.Where("id = ? AND space_id = ? AND deleted_at IS NULL", taskID, spaceID).First(&task).Error; err != nil {
		bizErr(c, service.NewBizError(40008, "任务不存在", http.StatusNotFound))
		return
	}

	var sources []model.SummarySource
	h.db.Where("task_id = ?", taskID).Find(&sources)

	srcList := make([]gin.H, 0, len(sources))
	for _, s := range sources {
		srcList = append(srcList, gin.H{
			"source_type": s.SourceType,
			"source_id":   s.SourceID,
			"source_name": s.SourceName,
		})
	}

	var participants []model.SummaryParticipant
	h.db.Where("task_id = ?", taskID).Find(&participants)

	partList := make([]gin.H, 0, len(participants))
	for _, p := range participants {
		item := gin.H{
			"user_id":   p.UserID,
			"user_name": p.UserName,
			"status":    p.Status,
		}
		if p.ConfirmedAt != nil {
			item["confirmed_at"] = p.ConfirmedAt.Format(time.RFC3339)
		}
		partList = append(partList, item)
	}

	var latestResult model.SummaryResult
	h.db.Where("task_id = ?", taskID).Order("version DESC").Limit(1).Find(&latestResult)

	var resultOut interface{}
	if latestResult.ID > 0 {
		resultOut = gin.H{
			"content":          latestResult.Content,
			"total_msg_count":  latestResult.TotalMsgCount,
			"total_token_used": latestResult.TotalTokenUsed,
			"model_version":    latestResult.ModelVersion,
			"version":          latestResult.Version,
			"generated_at":     latestResult.GeneratedAt.Format(time.RFC3339),
		}
	}

	ok(c, gin.H{
		"task_id":          task.ID,
		"task_no":          task.TaskNo,
		"title":            task.Title,
		"summary_mode":     task.SummaryMode,
		"status":           task.Status,
		"trigger_type":     task.TriggerType,
		"time_range_start": task.TimeRangeStart.Format(time.RFC3339),
		"time_range_end":   task.TimeRangeEnd.Format(time.RFC3339),
		"sources":          srcList,
		"participants":     partList,
		"result":           resultOut,
		"error_message":    task.ErrorMessage,
		"created_at":       task.CreatedAt.Format(time.RFC3339),
		"updated_at":       task.UpdatedAt.Format(time.RFC3339),
	})
}

// GetResult handles GET /api/v1/summaries/:id/result
func (h *TaskHandler) GetResult(c *gin.Context) {
	spaceID := middleware.GetSpaceID(c)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "invalid task id"})
		return
	}

	var task model.SummaryTask
	if err := h.db.Where("id = ? AND space_id = ? AND deleted_at IS NULL", taskID, spaceID).First(&task).Error; err != nil {
		bizErr(c, service.NewBizError(40008, "任务不存在", http.StatusNotFound))
		return
	}

	var result model.SummaryResult
	if err := h.db.Where("task_id = ?", taskID).Order("version DESC").Limit(1).First(&result).Error; err != nil {
		bizErr(c, service.NewBizError(40008, "暂无结果", http.StatusNotFound))
		return
	}

	ok(c, gin.H{
		"content":          result.Content,
		"total_msg_count":  result.TotalMsgCount,
		"total_token_used": result.TotalTokenUsed,
		"model_version":    result.ModelVersion,
		"version":          result.Version,
		"generated_at":     result.GeneratedAt.Format(time.RFC3339),
	})
}

// Regenerate handles POST /api/v1/summaries/:id/regenerate
func (h *TaskHandler) Regenerate(c *gin.Context) {
	spaceID := middleware.GetSpaceID(c)
	userID := middleware.GetUserID(c)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "invalid task id"})
		return
	}

	var task model.SummaryTask
	if err := h.db.Where("id = ? AND space_id = ? AND deleted_at IS NULL", taskID, spaceID).First(&task).Error; err != nil {
		bizErr(c, service.NewBizError(40008, "任务不存在", http.StatusNotFound))
		return
	}
	if task.CreatorID != userID {
		bizErr(c, service.NewBizError(40004, "仅创建者可重新生成", http.StatusForbidden))
		return
	}
	if task.Status != model.StatusCompleted && task.Status != model.StatusFailed && task.Status != model.StatusCancelled {
		bizErr(c, service.NewBizError(40005, "任务状态不允许此操作", http.StatusConflict))
		return
	}

	nextVer, _ := service.GetNextVersion(h.db, taskID)

	h.db.Where("task_id = ?", taskID).Delete(&model.SummaryChunk{})
	h.db.Model(&task).Updates(map[string]interface{}{
		"status":              model.StatusPending,
		"retry_count":         0,
		"error_message":       nil,
		"processing_deadline": nil,
	})

	ok(c, gin.H{
		"task_id":     task.ID,
		"status":      model.StatusPending,
		"new_version": nextVer,
	})
}

// InferScope handles GET /api/v1/summary-infer
func (h *TaskHandler) InferScope(c *gin.Context) {
	topic := c.Query("topic")
	if topic == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 40000, Message: "topic is required"})
		return
	}
	result := service.InferScope(topic)
	ok(c, result)
}
