package handler

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/service"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/timezone"
)

func TestPR62Round9_Toggle_ReenableUsesNextRunInitial_TodayWhenAhead(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	now := timezone.Now()
	if now.Hour() == 23 && now.Minute() >= 58 {
		t.Skip("too close to midnight for a stable same-day future run_time assertion")
	}
	future := now.Add(2 * time.Minute)
	runTime := future.Format("15:04")

	sched := model.SummarySchedule{
		SpaceID:       "space1",
		CreatorID:     "creator1",
		Title:         "today",
		SummaryMode:   model.ModeByPerson,
		IntervalDays:  1,
		RunTime:       runTime,
		TimeRangeType: 2,
		IsActive:      0,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	db.Model(&sched).Update("is_active", 0)
	taskNow := time.Now().UTC()
	task := model.SummaryTask{TaskNo: "toggle-today", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: taskNow, TimeRangeEnd: taskNow, ScheduleID: &sched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator1", Status: model.ParticipantAccepted}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID)+"/toggle", map[string]interface{}{"is_active": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got model.SummarySchedule
	if err := db.First(&got, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	want, err := service.NextRunInitial("", 1, 0, runTime, 0, 0, timezone.Now())
	if err != nil {
		t.Fatalf("want next_run: %v", err)
	}
	if got.NextRunAt == nil || !sameDateInLocation(*got.NextRunAt, want) {
		t.Fatalf("next_run_at=%v want same day as %v", got.NextRunAt, want)
	}
}

func TestPR62Round9_Update_MonthlyRunTimeChangePreservesAnchorDOM(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	nextRun := timezone.Now().Add(24 * time.Hour)
	sched := model.SummarySchedule{
		SpaceID:        "space1",
		CreatorID:      "creator1",
		Title:          "monthly",
		SummaryMode:    model.ModeByPerson,
		IntervalMonths: 1,
		RunTime:        "09:00",
		DayOfMonth:     0,
		AnchorDOM:      30,
		TimeRangeType:  2,
		IsActive:       1,
		NextRunAt:      &nextRun,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	taskNow := time.Now().UTC()
	task := model.SummaryTask{TaskNo: "monthly-anchor-update", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: taskNow, TimeRangeEnd: taskNow, ScheduleID: &sched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator1", Status: model.ParticipantAccepted}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID), map[string]interface{}{
		"run_time": "10:30",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got model.SummarySchedule
	if err := db.First(&got, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if got.AnchorDOM != 30 {
		t.Fatalf("anchor_dom=%d want 30", got.AnchorDOM)
	}
}

func TestPR62Round9_CreateReuseMonthlyRunTimeChangePreservesAnchorDOM(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	nextRun := timezone.Now().Add(24 * time.Hour)
	sched := model.SummarySchedule{
		SpaceID:        "space1",
		CreatorID:      "creator1",
		Title:          "existing",
		SummaryMode:    model.ModeByPerson,
		IntervalMonths: 1,
		RunTime:        "09:00",
		DayOfMonth:     0,
		AnchorDOM:      30,
		TimeRangeType:  2,
		IsActive:       1,
		NextRunAt:      &nextRun,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	taskNow := time.Now().UTC()
	task := model.SummaryTask{TaskNo: "monthly-anchor-create", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: taskNow, TimeRangeEnd: taskNow, ScheduleID: &sched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator1", Status: model.ParticipantAccepted}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"title":           "reuse",
		"scope":           "task",
		"task_id":         task.ID,
		"interval_months": 1,
		"day_of_month":    0,
		"run_time":        "10:30",
		"time_range_type": 2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got model.SummarySchedule
	if err := db.First(&got, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if got.AnchorDOM != 30 {
		t.Fatalf("anchor_dom=%d want 30", got.AnchorDOM)
	}
}

func TestPR62Round9_Update_MonthlyExplicitDOMZeroKeepsExistingAnchorDOM(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	nextRun := timezone.Now().Add(24 * time.Hour)
	sched := model.SummarySchedule{
		SpaceID:        "space1",
		CreatorID:      "creator1",
		Title:          "monthly-dom-change",
		SummaryMode:    model.ModeByPerson,
		IntervalMonths: 1,
		RunTime:        "09:00",
		DayOfMonth:     31,
		AnchorDOM:      31,
		TimeRangeType:  2,
		IsActive:       1,
		NextRunAt:      &nextRun,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	taskNow := time.Now().UTC()
	task := model.SummaryTask{TaskNo: "monthly-anchor-zero", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: taskNow, TimeRangeEnd: taskNow, ScheduleID: &sched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator1", Status: model.ParticipantAccepted}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID), map[string]interface{}{
		"day_of_month": 0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got model.SummarySchedule
	if err := db.First(&got, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if got.DayOfMonth != 0 {
		t.Fatalf("day_of_month=%d want 0", got.DayOfMonth)
	}
	if got.AnchorDOM != 31 {
		t.Fatalf("anchor_dom=%d want 31", got.AnchorDOM)
	}
}

func TestPR62Round9_Toggle_ReenableUsesNextRunInitial_NextCycleWhenPassed(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	now := timezone.Now()
	past := now.Add(-2 * time.Minute)
	runTime := past.Format("15:04")

	sched := model.SummarySchedule{
		SpaceID:       "space1",
		CreatorID:     "creator1",
		Title:         "tomorrow",
		SummaryMode:   model.ModeByPerson,
		IntervalDays:  1,
		RunTime:       runTime,
		TimeRangeType: 2,
		IsActive:      0,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	db.Model(&sched).Update("is_active", 0)
	taskNow := time.Now().UTC()
	task := model.SummaryTask{TaskNo: "toggle-next", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: taskNow, TimeRangeEnd: taskNow, ScheduleID: &sched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator1", Status: model.ParticipantAccepted}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID)+"/toggle", map[string]interface{}{"is_active": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got model.SummarySchedule
	if err := db.First(&got, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	want, err := service.NextRunInitial("", 1, 0, runTime, 0, 0, timezone.Now())
	if err != nil {
		t.Fatalf("want next_run: %v", err)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at=%v want %v", got.NextRunAt, want)
	}
}

func TestPR62Round9_Toggle_ReenableRejectsOrphanBinding(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	sched := model.SummarySchedule{
		SpaceID:       "space1",
		CreatorID:     "creator1",
		Title:         "orphan",
		SummaryMode:   model.ModeByPerson,
		IntervalDays:  1,
		RunTime:       "17:00",
		TimeRangeType: 2,
		IsActive:      0,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	db.Model(&sched).Update("is_active", 0)

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID)+"/toggle", map[string]interface{}{"is_active": true})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if code := decodeCode(t, w.Body); code != 40008 {
		t.Fatalf("code=%d want 40008 body=%s", code, w.Body.String())
	}
}

func TestPR62Round9_Toggle_ReenableRejectsBindingOwnedByAnotherCreator(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	sched := model.SummarySchedule{
		SpaceID:           "space1",
		CreatorID:         "creator1",
		Title:             "creator mismatch",
		SummaryMode:       model.ModeByPerson,
		IntervalDays:      1,
		RunTime:           "17:00",
		TimeRangeType:     2,
		IsActive:          0,
		ParticipantConfig: mustParticipantConfig(t, [2]string{"creator1", "Creator One"}),
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	db.Model(&sched).Update("is_active", 0)
	taskNow := time.Now().UTC()
	task := model.SummaryTask{TaskNo: "toggle-owner", SpaceID: "space1", CreatorID: "creator2", SummaryMode: model.ModeByPerson, TimeRangeStart: taskNow, TimeRangeEnd: taskNow, ScheduleID: &sched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&model.SummaryParticipant{TaskID: task.ID, UserID: "creator2", Status: model.ParticipantAccepted}).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID)+"/toggle", map[string]interface{}{"is_active": true})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if code := decodeCode(t, w.Body); code != 40004 {
		t.Fatalf("code=%d want 40004 body=%s", code, w.Body.String())
	}
}

func TestPR62Round9_DeleteSummary_LockOrderScheduleBeforeTask(t *testing.T) {
	src, err := os.ReadFile("task.go")
	if err != nil {
		t.Fatalf("read task.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func (h *TaskHandler) DeleteSummary")
	end := strings.Index(body[start:], "func (h *TaskHandler) CancelSummary")
	if start < 0 || end < 0 {
		t.Fatalf("failed to isolate DeleteSummary body")
	}
	fn := body[start : start+end]

	peekPos := strings.Index(fn, "peekTaskScheduleID")
	schedLockPos := strings.Index(fn, "First(&sched)")
	taskLockPos := strings.Index(fn, "First(&liveTask)")
	if peekPos < 0 || schedLockPos < 0 || taskLockPos < 0 {
		t.Fatalf("expected peek + schedule lock + task lock in DeleteSummary")
	}
	if !(peekPos < schedLockPos && schedLockPos < taskLockPos) {
		t.Fatalf("DeleteSummary lock order must be schedule->task (peek=%d sched=%d task=%d)", peekPos, schedLockPos, taskLockPos)
	}
}

func sameDateInLocation(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
