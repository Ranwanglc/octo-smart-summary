package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
)

// --- Bug 1 (PR#62 Jerry-Xin r3) ---------------------------------------------
// Rebuilding a schedule from the detail page goes through CreateSchedule's
// reuse-existing-binding branch. Before the fix that branch updated the row but
// left is_active=0, so the scheduler never picked it up. Verify the reused
// schedule is re-activated and gets a sane (future) next_run_at.

func TestCreateSchedule_ReuseInactiveBinding_Reactivates(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	// Existing schedule that the task is bound to, but currently INACTIVE
	// (mirrors the state after the detail page hid the schedule_id).
	sched := model.SummarySchedule{
		SpaceID:       "space1",
		CreatorID:     "creator1",
		Title:         "Inactive existing",
		SummaryMode:   model.ModeByPerson,
		IntervalDays:  1,
		RunTime:       "23:30",
		TimeRangeType: 2,
		IsActive:      0,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	// Force is_active=0 (GORM default is 1).
	if err := db.Model(&sched).Update("is_active", 0).Error; err != nil {
		t.Fatalf("deactivate schedule: %v", err)
	}
	now := time.Now().UTC()
	task := model.SummaryTask{
		TaskNo:         "PR62-R3-REACTIVATE",
		SpaceID:        "space1",
		CreatorID:      "creator1",
		SummaryMode:    model.ModeByPerson,
		TimeRangeStart: now,
		TimeRangeEnd:   now,
		ScheduleID:     &sched.ID,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPost, "/api/v1/summary-schedules", map[string]interface{}{
		"title":           "Rebuilt from detail page",
		"scope":           "task",
		"task_id":         task.ID,
		"interval_days":   1,
		"run_time":        "23:30",
		"time_range_type": 2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if int64(resp.Data["schedule_id"].(float64)) != sched.ID {
		t.Fatalf("expected reuse of schedule %d, got %v", sched.ID, resp.Data["schedule_id"])
	}
	// next_run_at must be present and non-empty (returned to the user as the
	// scheduled time). The actual firing depends on is_active below.
	if nr, _ := resp.Data["next_run_at"].(string); nr == "" {
		t.Fatalf("expected non-empty next_run_at, got %q", nr)
	}

	var got model.SummarySchedule
	if err := db.First(&got, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	// THE FIX: schedule must be reactivated so the scheduler picks it up.
	if got.IsActive != 1 {
		t.Fatalf("expected schedule reactivated (is_active=1), got %d", got.IsActive)
	}
	if got.Title != "Rebuilt from detail page" {
		t.Fatalf("expected title updated, got %q", got.Title)
	}
	// next_run_at must be set and in the future (first-run semantics, not a
	// skipped slot in the past).
	if got.NextRunAt == nil {
		t.Fatalf("expected next_run_at set")
	}
	if !got.NextRunAt.After(now) {
		t.Fatalf("expected next_run_at in the future, got %v (now=%v)", got.NextRunAt, now)
	}

	// No second schedule row created.
	var count int64
	db.Model(&model.SummarySchedule{}).Where("deleted_at IS NULL").Count(&count)
	if count != 1 {
		t.Fatalf("expected single schedule row, live count=%d", count)
	}
}

// --- Bug 2 (PR#62 Jerry-Xin r3) ---------------------------------------------
// UpdateSchedule scope=task rebind soft-deletes the OLD schedule. That delete
// must respect the same ownership + exclusivity guard as DeleteSummary's
// cascade: only soft-delete when the caller is the old schedule's creator AND
// no other live task still binds it. Otherwise unbind-only (leave old alive).

// Case A: caller is NOT the old schedule's creator -> do not soft-delete it.
func TestUpdateSchedule_RebindDoesNotDeleteOthersOldSchedule(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	now := time.Now().UTC()
	// Old schedule owned by victimUser; task owned by attacker but bound to it.
	oldSched := model.SummarySchedule{SpaceID: "space1", CreatorID: "victimUser", Title: "Victim old", SummaryMode: model.ModeByPerson, IntervalDays: 1, RunTime: "17:00", IsActive: 1}
	// New schedule owned by attacker (so UpdateSchedule's creator check on the
	// TARGET schedule passes and we reach the old-schedule cleanup branch).
	newSched := model.SummarySchedule{SpaceID: "space1", CreatorID: "attacker", Title: "Attacker new", SummaryMode: model.ModeByPerson, IntervalDays: 1, RunTime: "11:00", IsActive: 1}
	if err := db.Create(&oldSched).Error; err != nil {
		t.Fatalf("create old sched: %v", err)
	}
	if err := db.Create(&newSched).Error; err != nil {
		t.Fatalf("create new sched: %v", err)
	}
	task := model.SummaryTask{TaskNo: "PR62-R3-REBIND-A", SpaceID: "space1", CreatorID: "attacker", SummaryMode: model.ModeByPerson, TimeRangeStart: now, TimeRangeEnd: now, ScheduleID: &oldSched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	w := doScheduleJSONRequestAsUser(t, r, "attacker", "space1", http.MethodPut, "/api/v1/summary-schedules/"+itoa(newSched.ID), map[string]interface{}{
		"scope":         "task",
		"task_id":       task.ID,
		"run_time":      "08:15",
		"interval_days": 1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// Task is rebound to the new schedule.
	var gotTask model.SummaryTask
	if err := db.First(&gotTask, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if gotTask.ScheduleID == nil || *gotTask.ScheduleID != newSched.ID {
		t.Fatalf("expected task rebound to new schedule %d, got %v", newSched.ID, gotTask.ScheduleID)
	}

	// THE FIX: victim's old schedule MUST survive (caller not its creator).
	var gotOld model.SummarySchedule
	if err := db.Where("id = ? AND deleted_at IS NULL", oldSched.ID).First(&gotOld).Error; err != nil {
		t.Fatalf("victim old schedule must NOT be soft-deleted by a non-creator: %v", err)
	}
}

// Case B: caller IS the creator but the old schedule is still bound by another
// live task -> do not soft-delete it (exclusivity guard).
func TestUpdateSchedule_RebindDoesNotDeleteStillBoundOldSchedule(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	now := time.Now().UTC()
	oldSched := model.SummarySchedule{SpaceID: "space1", CreatorID: "creator1", Title: "Shared old", SummaryMode: model.ModeByPerson, IntervalDays: 1, RunTime: "17:00", IsActive: 1}
	newSched := model.SummarySchedule{SpaceID: "space1", CreatorID: "creator1", Title: "New", SummaryMode: model.ModeByPerson, IntervalDays: 1, RunTime: "11:00", IsActive: 1}
	if err := db.Create(&oldSched).Error; err != nil {
		t.Fatalf("create old sched: %v", err)
	}
	if err := db.Create(&newSched).Error; err != nil {
		t.Fatalf("create new sched: %v", err)
	}
	// task is being rebound; otherTask keeps the old schedule alive.
	task := model.SummaryTask{TaskNo: "PR62-R3-REBIND-B", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: now, TimeRangeEnd: now, ScheduleID: &oldSched.ID}
	otherTask := model.SummaryTask{TaskNo: "PR62-R3-OTHER", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: now, TimeRangeEnd: now, ScheduleID: &oldSched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Create(&otherTask).Error; err != nil {
		t.Fatalf("create other task: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(newSched.ID), map[string]interface{}{
		"scope":         "task",
		"task_id":       task.ID,
		"run_time":      "08:15",
		"interval_days": 1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// THE FIX: old schedule still bound by otherTask -> must survive.
	var gotOld model.SummarySchedule
	if err := db.Where("id = ? AND deleted_at IS NULL", oldSched.ID).First(&gotOld).Error; err != nil {
		t.Fatalf("old schedule still bound by another task must NOT be soft-deleted: %v", err)
	}
	// otherTask must still be bound to it.
	var gotOther model.SummaryTask
	if err := db.First(&gotOther, otherTask.ID).Error; err != nil {
		t.Fatalf("reload other task: %v", err)
	}
	if gotOther.ScheduleID == nil || *gotOther.ScheduleID != oldSched.ID {
		t.Fatalf("expected other task to stay on old schedule %d, got %v", oldSched.ID, gotOther.ScheduleID)
	}
	// rebound task moved to the new schedule.
	var gotTask model.SummaryTask
	if err := db.First(&gotTask, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if gotTask.ScheduleID == nil || *gotTask.ScheduleID != newSched.ID {
		t.Fatalf("expected task rebound to new schedule %d, got %v", newSched.ID, gotTask.ScheduleID)
	}
}

// Case C (happy path): caller IS the creator AND the old schedule is now
// exclusively unbound -> it IS soft-deleted (legacy rebind cleanup preserved).
func TestUpdateSchedule_RebindCreatorExclusiveSoftDeletesOldSchedule(t *testing.T) {
	db := setupScheduleDB(t)
	h := NewScheduleHandler(db)
	r := setupScheduleRouter(h)

	now := time.Now().UTC()
	oldSched := model.SummarySchedule{SpaceID: "space1", CreatorID: "creator1", Title: "Old", SummaryMode: model.ModeByPerson, IntervalDays: 1, RunTime: "17:00", IsActive: 1}
	newSched := model.SummarySchedule{SpaceID: "space1", CreatorID: "creator1", Title: "New", SummaryMode: model.ModeByPerson, IntervalDays: 1, RunTime: "11:00", IsActive: 1}
	if err := db.Create(&oldSched).Error; err != nil {
		t.Fatalf("create old sched: %v", err)
	}
	if err := db.Create(&newSched).Error; err != nil {
		t.Fatalf("create new sched: %v", err)
	}
	task := model.SummaryTask{TaskNo: "PR62-R3-REBIND-C", SpaceID: "space1", CreatorID: "creator1", SummaryMode: model.ModeByPerson, TimeRangeStart: now, TimeRangeEnd: now, ScheduleID: &oldSched.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(newSched.ID), map[string]interface{}{
		"scope":         "task",
		"task_id":       task.ID,
		"run_time":      "08:15",
		"interval_days": 1,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// Old schedule must be soft-deleted (creator + exclusive).
	var cnt int64
	db.Model(&model.SummarySchedule{}).Where("id = ? AND deleted_at IS NULL", oldSched.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("expected old schedule soft-deleted by its sole-owner creator, live count=%d", cnt)
	}
	var gotOld model.SummarySchedule
	if err := db.First(&gotOld, oldSched.ID).Error; err != nil {
		t.Fatalf("reload old sched: %v", err)
	}
	if gotOld.DeletedAt == nil {
		t.Fatalf("expected old schedule DeletedAt set")
	}
}
