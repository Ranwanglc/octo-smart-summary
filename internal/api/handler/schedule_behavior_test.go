package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/middleware"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Self-contained harness for the scheduled-summary handler behavior tests.
//
// These tests exercise the HTTP contract of the schedule endpoints against an
// in-memory sqlite DB. Note: sqlite cannot enforce the MySQL generated-column
// live-binding unique index, so binding invariants are asserted through the
// handler's own application-level guards (the path real callers hit), not the
// DB constraint. The DB constraint itself is validated separately against a
// real MySQL 8.0 instance (see migrations/sql validation).
// ---------------------------------------------------------------------------

func newScheduleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.SummaryTask{},
		&model.SummarySchedule{},
		&model.SummaryParticipant{},
		&model.SummaryResult{},
		&model.SummaryChunk{},
		&model.PersonalResult{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newScheduleTestRouter(db *gorm.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.AuthMiddleware(&mockTokenResolver{}), middleware.SpaceMiddleware())
	sh := NewScheduleHandler(db)
	th := NewTaskHandler(db, db, "")
	r.POST("/api/v1/summary-schedules", sh.CreateSchedule)
	r.PUT("/api/v1/summary-schedules/:id", sh.UpdateSchedule)
	r.DELETE("/api/v1/summary-schedules/:id", sh.DeleteSchedule)
	r.PUT("/api/v1/summary-schedules/:id/toggle", sh.ToggleSchedule)
	r.DELETE("/api/v1/summaries/:id", th.DeleteSummary)
	return r
}

func scheduleReq(t *testing.T, r *gin.Engine, userID, spaceID, method, path string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Token", userID)
	req.Header.Set("X-Space-Id", spaceID)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedScheduleTask(t *testing.T, db *gorm.DB, taskNo, space, creator string) int64 {
	t.Helper()
	now := time.Now().UTC()
	task := model.SummaryTask{
		TaskNo: taskNo, SpaceID: space, CreatorID: creator,
		SummaryMode: model.ModeByPerson, Status: model.StatusCompleted,
		TimeRangeStart: now, TimeRangeEnd: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	// The single-person guard counts participants; a sole creator participant
	// keeps the task single-person.
	db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: creator, UserName: "C"})
	return task.ID
}

func sid(v int64) string { return strconv.FormatInt(v, 10) }

// ---------------------------------------------------------------------------
// Create: validation contract.
// ---------------------------------------------------------------------------

func TestCreateSchedule_RejectsCronWrite(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "cron_expr": "0 9 * * *",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting cron write, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSchedule_RejectsNonTaskScope(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)

	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"interval_days": 1, "run_time": "09:00",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing scope=task, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSchedule_RejectsMalformedRunTime(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 1, "run_time": "9:0",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed run_time, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSchedule_RejectsAnchorModeMismatch(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	// day_of_month is only valid in month mode; supplying it in day mode fails.
	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 3, "run_time": "09:00", "day_of_month": 5,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for day-mode + day_of_month, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSchedule_BindsUnscheduledTask(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 7, "run_time": "09:00", "day_of_week": 1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 binding schedule, got %d: %s", w.Code, w.Body.String())
	}
	var task model.SummaryTask
	if err := db.First(&task, taskID).Error; err != nil {
		t.Fatal(err)
	}
	if task.ScheduleID == nil {
		t.Fatal("task should be bound to a schedule after create")
	}
}

// ---------------------------------------------------------------------------
// Single-person invariant: scheduled summary rejects multi-person tasks.
// ---------------------------------------------------------------------------

func TestCreateSchedule_MultiPersonTaskRejected(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")
	// Add a second, non-creator participant -> task is multi-person.
	db.Create(&model.SummaryParticipant{TaskID: taskID, UserID: "other", UserName: "O"})

	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 1, "run_time": "09:00",
	})
	if w.Code == http.StatusOK {
		t.Fatalf("expected rejection for multi-person task, got 200: %s", w.Body.String())
	}
}

func TestCreateSchedule_RebindIdempotentForSameTask(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	// First create binds a schedule.
	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 1, "run_time": "09:00",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("first create: %d %s", w.Code, w.Body.String())
	}
	// A second create for the same task must reuse/update the existing schedule
	// rather than create a second one (one-to-one binding).
	w = scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 2, "run_time": "10:00",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("second create: %d %s", w.Code, w.Body.String())
	}
	var count int64
	db.Model(&model.SummarySchedule{}).Where("deleted_at IS NULL").Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 live schedule, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Cascade delete authorization: deleting a summary may only cascade-delete a
// bound schedule when the caller owns the schedule; otherwise it unbinds.
// ---------------------------------------------------------------------------

func TestDeleteSummary_CreatorCascadeDeletesOwnSchedule(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 1, "run_time": "09:00",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bind: %d %s", w.Code, w.Body.String())
	}
	var task model.SummaryTask
	db.First(&task, taskID)
	schedID := *task.ScheduleID

	// Creator deletes their own summary -> the schedule is cascade soft-deleted.
	w = scheduleReq(t, r, "u1", "s1", http.MethodDelete, "/api/v1/summaries/"+sid(taskID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete summary: %d %s", w.Code, w.Body.String())
	}
	var sched model.SummarySchedule
	if err := db.Unscoped().First(&sched, schedID).Error; err != nil {
		t.Fatal(err)
	}
	if sched.DeletedAt == nil {
		t.Error("schedule should be cascade soft-deleted by its creator")
	}
}

// ---------------------------------------------------------------------------
// Toggle: re-enabling a schedule with an invalid recurrence is rejected.
// ---------------------------------------------------------------------------

func TestToggleSchedule_ReenableInvalidRecurrenceRejected(t *testing.T) {
	db := newScheduleTestDB(t)
	r := newScheduleTestRouter(db)
	taskID := seedScheduleTask(t, db, "T1", "s1", "u1")

	// Bind a valid schedule via the API so the task<->schedule link exists.
	w := scheduleReq(t, r, "u1", "s1", http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"scope": "task", "task_id": taskID, "interval_days": 1, "run_time": "09:00",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bind: %d %s", w.Code, w.Body.String())
	}
	var task model.SummaryTask
	db.First(&task, taskID)
	schedID := *task.ScheduleID

	// Force the schedule inactive and strip every recurrence source, simulating
	// a row that can no longer compute a next_run.
	if err := db.Model(&model.SummarySchedule{}).Where("id = ?", schedID).Updates(map[string]interface{}{
		"is_active": 0, "interval_days": 0, "interval_months": 0, "cron_expr": "",
	}).Error; err != nil {
		t.Fatal(err)
	}

	w = scheduleReq(t, r, "u1", "s1", http.MethodPut, "/api/v1/summary-schedules/"+sid(schedID)+"/toggle", map[string]interface{}{
		"is_active": true,
	})
	if w.Code == http.StatusOK {
		t.Fatalf("expected rejection re-enabling invalid-recurrence schedule, got 200: %s", w.Body.String())
	}
}
