package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
)

func TestPR62Round14_EditSummary_PausesBoundScheduleAndUsesDisplayResult(t *testing.T) {
	db := setupEditDB(t)

	sched := model.SummarySchedule{
		SpaceID:      "space1",
		CreatorID:    "creator1",
		Title:        "daily",
		SummaryMode:  model.ModeByPerson,
		IntervalDays: 1,
		RunTime:      "09:00",
		IsActive:     1,
	}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	task := model.SummaryTask{
		TaskNo:      "TST-R14-EDIT-BOUND",
		SpaceID:     "space1",
		CreatorID:   "creator1",
		SummaryMode: model.ModeByPerson,
		Status:      model.StatusCompleted,
		ScheduleID:  &sched.ID,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	participant := model.SummaryParticipant{
		TaskID:   task.ID,
		UserID:   "creator1",
		UserName: "Creator",
		Status:   model.ParticipantCompleted,
	}
	if err := db.Create(&participant).Error; err != nil {
		t.Fatalf("create participant: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	personal := model.PersonalResult{
		TaskID:           task.ID,
		ParticipantRefID: participant.ID,
		UserID:           "creator1",
		WorkerStatus:     model.PersonalStatusCompleted,
		Content:          "visible edited summary",
		GeneratedAt:      &now,
	}
	if err := db.Create(&personal).Error; err != nil {
		t.Fatalf("create personal result: %v", err)
	}
	editedAt := now.Add(-15 * time.Minute)
	visible := model.SummaryResult{
		TaskID:      task.ID,
		Content:     "visible edited summary",
		Version:     1,
		EditedAt:    &editedAt,
		GeneratedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&visible).Error; err != nil {
		t.Fatalf("create visible result: %v", err)
	}
	hidden := model.SummaryResult{
		TaskID:      task.ID,
		Content:     "new auto summary that should stay untouched",
		Version:     2,
		GeneratedAt: now,
	}
	if err := db.Create(&hidden).Error; err != nil {
		t.Fatalf("create hidden result: %v", err)
	}

	r := setupEditRouter(NewEditHandler(db))
	w := doEditRequest(r, task.ID, "creator1", map[string]interface{}{
		"content":        "creator final summary",
		"base_result_id": visible.ID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var gotVisible model.SummaryResult
	if err := db.First(&gotVisible, visible.ID).Error; err != nil {
		t.Fatalf("reload visible result: %v", err)
	}
	if gotVisible.Content != "creator final summary" {
		t.Fatalf("visible result content=%q want creator final summary", gotVisible.Content)
	}
	if gotVisible.EditedAt == nil {
		t.Fatalf("visible result should remain edited")
	}

	var gotHidden model.SummaryResult
	if err := db.First(&gotHidden, hidden.ID).Error; err != nil {
		t.Fatalf("reload hidden result: %v", err)
	}
	if gotHidden.Content != hidden.Content {
		t.Fatalf("hidden result content=%q want %q", gotHidden.Content, hidden.Content)
	}

	var gotPersonal model.PersonalResult
	if err := db.First(&gotPersonal, personal.ID).Error; err != nil {
		t.Fatalf("reload personal result: %v", err)
	}
	if gotPersonal.Content != "creator final summary" {
		t.Fatalf("personal result content=%q want creator final summary", gotPersonal.Content)
	}

	var gotSched model.SummarySchedule
	if err := db.First(&gotSched, sched.ID).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if gotSched.IsActive != 0 {
		t.Fatalf("schedule is_active=%d want 0", gotSched.IsActive)
	}
}

func TestPR62Round14_EditSummary_WithoutBoundScheduleLeavesOtherSchedulesUntouched(t *testing.T) {
	db := setupEditDB(t)
	taskID, resultID, _ := seedEditableTask(t, db)

	unrelated := model.SummarySchedule{
		SpaceID:      "space1",
		CreatorID:    "creator1",
		Title:        "unrelated",
		SummaryMode:  model.ModeByPerson,
		IntervalDays: 1,
		RunTime:      "10:00",
		IsActive:     1,
	}
	if err := db.Create(&unrelated).Error; err != nil {
		t.Fatalf("create unrelated schedule: %v", err)
	}

	r := setupEditRouter(NewEditHandler(db))
	w := doEditRequest(r, taskID, "creator1", map[string]interface{}{
		"content":        "updated content without schedule",
		"base_result_id": resultID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got model.SummarySchedule
	if err := db.First(&got, unrelated.ID).Error; err != nil {
		t.Fatalf("reload unrelated schedule: %v", err)
	}
	if got.IsActive != 1 {
		t.Fatalf("unrelated schedule is_active=%d want 1", got.IsActive)
	}
}

func TestPR62Round14_DeleteSummary_PausesScheduleWhenParticipantUnbinds(t *testing.T) {
	db := setupDeleteCascadeDB(t)
	taskID, schedID := seedTaskWithSchedule(t, db, "creator1", "creator1", "user2")
	r := setupDeleteCascadeRouter(NewTaskHandler(db, db, ""))

	w := doDelete(t, r, taskID, "user2")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var task model.SummaryTask
	if err := db.Unscoped().First(&task, taskID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.DeletedAt == nil {
		t.Fatalf("task should be soft-deleted")
	}
	if task.ScheduleID != nil {
		t.Fatalf("task should be unbound, got schedule_id=%v", *task.ScheduleID)
	}

	var sched model.SummarySchedule
	if err := db.Where("id = ? AND deleted_at IS NULL", schedID).First(&sched).Error; err != nil {
		t.Fatalf("reload schedule: %v", err)
	}
	if sched.IsActive != 0 {
		t.Fatalf("schedule is_active=%d want 0", sched.IsActive)
	}
}

func TestPR62Round14_UpdateSchedule_ClearsCarryForwardAnchorsOnModeSwitch(t *testing.T) {
	db := setupScheduleDB(t)
	r := setupScheduleRouter(NewScheduleHandler(db))

	tests := []struct {
		name             string
		schedule         model.SummarySchedule
		body             map[string]interface{}
		wantIntervalDays int
		wantMonths       int
		wantDOW          int
		wantDOM          int
	}{
		{
			name: "week to month clears day_of_week",
			schedule: model.SummarySchedule{
				SpaceID:       "space1",
				CreatorID:     "creator1",
				Title:         "weekly",
				SummaryMode:   model.ModeByPerson,
				IntervalDays:  7,
				RunTime:       "09:00",
				DayOfWeek:     3,
				TimeRangeType: 2,
				IsActive:      1,
			},
			body: map[string]interface{}{
				"interval_days":   0,
				"interval_months": 1,
			},
			wantMonths: 1,
			wantDOW:    0,
		},
		{
			name: "month to week clears day_of_month",
			schedule: model.SummarySchedule{
				SpaceID:        "space1",
				CreatorID:      "creator1",
				Title:          "monthly-to-weekly",
				SummaryMode:    model.ModeByPerson,
				IntervalMonths: 1,
				RunTime:        "09:00",
				DayOfMonth:     15,
				TimeRangeType:  2,
				IsActive:       1,
			},
			body: map[string]interface{}{
				"interval_months": 0,
				"interval_days":   7,
			},
			wantIntervalDays: 7,
			wantDOM:          0,
		},
		{
			name: "month to day clears day_of_month",
			schedule: model.SummarySchedule{
				SpaceID:        "space1",
				CreatorID:      "creator1",
				Title:          "monthly-to-daily",
				SummaryMode:    model.ModeByPerson,
				IntervalMonths: 1,
				RunTime:        "09:00",
				DayOfMonth:     28,
				TimeRangeType:  2,
				IsActive:       1,
			},
			body: map[string]interface{}{
				"interval_months": 0,
				"interval_days":   1,
			},
			wantIntervalDays: 1,
			wantDOM:          0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sched := tc.schedule
			if err := db.Create(&sched).Error; err != nil {
				t.Fatalf("create schedule: %v", err)
			}
			now := time.Now().UTC()
			task := model.SummaryTask{
				TaskNo:         "TST-R14-SWITCH-" + tc.name,
				SpaceID:        "space1",
				CreatorID:      "creator1",
				SummaryMode:    model.ModeByPerson,
				Status:         model.StatusCompleted,
				TimeRangeStart: now.Add(-time.Hour),
				TimeRangeEnd:   now,
				ScheduleID:     &sched.ID,
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatalf("create task: %v", err)
			}

			w := doScheduleJSONRequest(t, r, http.MethodPut, "/api/v1/summary-schedules/"+itoa(sched.ID), tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}

			var got model.SummarySchedule
			if err := db.First(&got, sched.ID).Error; err != nil {
				t.Fatalf("reload schedule: %v", err)
			}
			if got.IntervalDays != tc.wantIntervalDays {
				t.Fatalf("interval_days=%d want %d", got.IntervalDays, tc.wantIntervalDays)
			}
			if got.IntervalMonths != tc.wantMonths {
				t.Fatalf("interval_months=%d want %d", got.IntervalMonths, tc.wantMonths)
			}
			if got.DayOfWeek != tc.wantDOW {
				t.Fatalf("day_of_week=%d want %d", got.DayOfWeek, tc.wantDOW)
			}
			if got.DayOfMonth != tc.wantDOM {
				t.Fatalf("day_of_month=%d want %d", got.DayOfMonth, tc.wantDOM)
			}
		})
	}
}
