package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/middleware"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEditDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.AutoMigrate(
		&model.SummaryTask{},
		&model.SummarySource{},
		&model.SummaryParticipant{},
		&model.PersonalResult{},
		&model.SummaryResult{},
		&model.SummaryChunk{},
	)
	return db
}

func setupEditRouter(h *EditHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.AuthMiddleware(&mockTokenResolver{}), middleware.SpaceMiddleware())
	r.PUT("/api/v1/summaries/:id/edit", h.EditSummary)
	return r
}

func seedEditableTask(t *testing.T, db *gorm.DB) (taskID int64, resultID int64, prID int64) {
	t.Helper()
	now := time.Now().UTC()
	task := model.SummaryTask{
		TaskNo:      "TST-EDIT-001",
		SpaceID:     "space1",
		CreatorID:   "creator1",
		SummaryMode: model.ModeByPerson,
		Status:      model.StatusCompleted,
	}
	db.Create(&task)

	participant := model.SummaryParticipant{
		TaskID:   task.ID,
		UserID:   "creator1",
		UserName: "Creator",
		Status:   model.ParticipantCompleted,
	}
	db.Create(&participant)

	pr := model.PersonalResult{
		TaskID:           task.ID,
		ParticipantRefID: participant.ID,
		UserID:           "creator1",
		WorkerStatus:     model.PersonalStatusCompleted,
		Content:          "original content with [1] citation",
		CitationsJSON:    `[{"index":1,"sender":"Alice","content":"hello","sent_at":"2026-01-01T00:00:00Z","source":"grp","channel_id":"ch1","channel_type":2,"message_seq":100}]`,
		GeneratedAt:      &now,
	}
	db.Create(&pr)

	result := model.SummaryResult{
		TaskID:         task.ID,
		Content:        "original content with [1] citation",
		CitationsJSON:  `[{"index":1,"sender":"Alice","content":"hello","sent_at":"2026-01-01T00:00:00Z","source":"grp","channel_id":"ch1","channel_type":2,"message_seq":100}]`,
		TotalMsgCount:  10,
		TotalTokenUsed: 200,
		ModelVersion:   "test-v1",
		Version:        1,
		GeneratedAt:    now,
	}
	db.Create(&result)

	return task.ID, result.ID, pr.ID
}

func doEditRequest(r *gin.Engine, taskID int64, userID string, body interface{}) *httptest.ResponseRecorder {
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/summaries/%d/edit", taskID), bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("Token", userID)
	}
	req.Header.Set("X-Space-Id", "space1")
	r.ServeHTTP(w, req)
	return w
}

func doEditRequestWithSpace(r *gin.Engine, taskID int64, userID, spaceID string, body interface{}) *httptest.ResponseRecorder {
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/summaries/%d/edit", taskID), bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("Token", userID)
	}
	req.Header.Set("X-Space-Id", spaceID)
	r.ServeHTTP(w, req)
	return w
}

func TestEditSummary_WrongSpaceID(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "new content",
		"base_result_id": resultID,
	}
	w := doEditRequestWithSpace(r, taskID, "creator1", "wrong_space", body)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong space_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_WhitespaceOnlyContent(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	cases := []string{"   ", "\n\n", "\t\t", "  \n  \t  "}
	for _, content := range cases {
		body := map[string]interface{}{
			"content":        content,
			"base_result_id": resultID,
		}
		w := doEditRequest(r, taskID, "creator1", body)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for whitespace-only content %q, got %d: %s", content, w.Code, w.Body.String())
		}
	}
}

func TestEditSummary_Success(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, prID := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "updated content with [1] citation",
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["edited_at"] == nil {
		t.Error("expected edited_at to be set")
	}

	var sr model.SummaryResult
	db.First(&sr, resultID)
	if sr.Content != "updated content with [1] citation" {
		t.Errorf("SummaryResult content not updated: %q", sr.Content)
	}
	if sr.EditedAt == nil {
		t.Error("SummaryResult edited_at should be set")
	}

	var pr model.PersonalResult
	db.First(&pr, prID)
	if pr.Content != "updated content with [1] citation" {
		t.Errorf("PersonalResult content not updated: %q", pr.Content)
	}
	if pr.EditedAt == nil {
		t.Error("PersonalResult edited_at should be set")
	}
}

func TestEditSummary_CitationCleanup(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "updated content without any citation references",
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var sr model.SummaryResult
	db.First(&sr, resultID)
	citations := sr.GetCitations()
	if len(citations) != 0 {
		t.Errorf("expected 0 citations after cleanup, got %d", len(citations))
	}
}

func TestEditSummary_NonCreatorForbidden(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	db.Create(&model.SummaryParticipant{
		TaskID:   taskID,
		UserID:   "other_user",
		UserName: "Other",
	})

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "hacked content",
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "other_user", body)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_NonCompletedStatus(t *testing.T) {
	db := setupEditDB(t)
	now := time.Now().UTC()

	task := model.SummaryTask{
		TaskNo:      "TST-EDIT-002",
		SpaceID:     "space1",
		CreatorID:   "creator1",
		SummaryMode: model.ModeByPerson,
		Status:      model.StatusProcessing,
	}
	db.Create(&task)

	db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator1", UserName: "Creator"})

	result := model.SummaryResult{
		TaskID:      task.ID,
		Content:     "some content",
		Version:     1,
		GeneratedAt: now,
	}
	db.Create(&result)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "new content",
		"base_result_id": result.ID,
	}
	w := doEditRequest(r, task.ID, "creator1", body)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_BaseResultIDMismatch(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "new content",
		"base_result_id": resultID + 999,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_Idempotent(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "original content with [1] citation",
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for idempotent call, got %d: %s", w.Code, w.Body.String())
	}

	var sr model.SummaryResult
	db.First(&sr, resultID)
	if sr.EditedAt != nil {
		t.Error("edited_at should remain nil for idempotent (no-change) call")
	}
}

func TestEditSummary_EmptyContent(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "",
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty content, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_TaskNotFound(t *testing.T) {
	db := setupEditDB(t)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "some content",
		"base_result_id": 999,
	}
	w := doEditRequest(r, 99999, "creator1", body)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_MultiParticipantRejected(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	db.Create(&model.SummaryParticipant{
		TaskID:   taskID,
		UserID:   "participant2",
		UserName: "P2",
	})

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	body := map[string]interface{}{
		"content":        "new content",
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for multi-participant, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEditSummary_ContentTooLarge(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	h := NewEditHandler(db)
	r := setupEditRouter(h)

	largeContent := make([]byte, 500*1024+1)
	for i := range largeContent {
		largeContent[i] = 'a'
	}

	body := map[string]interface{}{
		"content":        string(largeContent),
		"base_result_id": resultID,
	}
	w := doEditRequest(r, taskID, "creator1", body)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized content, got %d: %s", w.Code, w.Body.String())
	}
}
