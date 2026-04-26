package worker

import (
	"encoding/json"
	"log"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/service"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// StartScheduler starts the 3 cron scan jobs (every 60s).
func StartScheduler(db *gorm.DB) *cron.Cron {
	c := cron.New()

	c.AddFunc("@every 60s", func() { scanPendingSchedules(db) })
	c.AddFunc("@every 60s", func() { scanConfirmTimeouts(db) })
	c.AddFunc("@every 60s", func() { scanStuckTasks(db) })

	c.Start()
	log.Println("[scheduler] started with 3 scan jobs (every 60s)")
	return c
}

// scanPendingSchedules creates tasks from due schedules.
func scanPendingSchedules(db *gorm.DB) {
	now := time.Now().UTC()
	var schedules []model.SummarySchedule
	err := db.Where("is_active = 1 AND next_run_at <= ? AND deleted_at IS NULL", now).Find(&schedules).Error
	if err != nil {
		log.Printf("[scheduler] query schedules: %v", err)
		return
	}

	for _, sched := range schedules {
		// Compute time range
		start, end := service.ComputeTimeRange(sched.TimeRangeType, now)

		// Parse source config
		var sources []struct {
			SourceType int    `json:"source_type"`
			SourceID   string `json:"source_id"`
		}
		if len(sched.SourceConfig) > 0 {
			json.Unmarshal([]byte(sched.SourceConfig), &sources)
		}

		taskNo := service.GenerateTaskNo()
		title := sched.Title
		if title == "" {
			title = "定时总结-" + taskNo[len(taskNo)-8:]
		}

		task := model.SummaryTask{
			TaskNo:         taskNo,
			SpaceID:        sched.SpaceID,
			CreatorID:      sched.CreatorID,
			Title:          title,
			SummaryMode:    sched.SummaryMode,
			TimeRangeStart: start,
			TimeRangeEnd:   end,
			Status:         model.StatusPending,
			TriggerType:    model.TriggerScheduled,
			ScheduleID:     &sched.ID,
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&task).Error; err != nil {
				return err
			}
			for _, s := range sources {
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
			return nil
		})
		if err != nil {
			log.Printf("[scheduler] create task for schedule %d: %v", sched.ID, err)
			continue
		}

		// Update schedule: last_run_at and next_run_at
		nextRun, err := service.NextRun(sched.CronExpr, now)
		if err != nil {
			log.Printf("[scheduler] compute next run for schedule %d: %v", sched.ID, err)
			continue
		}
		db.Model(&sched).Updates(map[string]interface{}{
			"last_run_at": now,
			"next_run_at": nextRun,
		})

		log.Printf("[scheduler] created task %d from schedule %d", task.ID, sched.ID)
	}
}

// scanConfirmTimeouts cancels tasks that exceeded the confirm deadline.
func scanConfirmTimeouts(db *gorm.DB) {
	now := time.Now().UTC()
	result := db.Model(&model.SummaryTask{}).
		Where("status = ? AND confirm_deadline < ?", model.StatusWaitingConfirm, now).
		Update("status", model.StatusCancelled)
	if result.RowsAffected > 0 {
		log.Printf("[scheduler] cancelled %d timed-out confirm tasks", result.RowsAffected)
	}
}

// scanStuckTasks resets tasks stuck in processing past their deadline.
func scanStuckTasks(db *gorm.DB) {
	now := time.Now().UTC()
	result := db.Model(&model.SummaryTask{}).
		Where("status = ? AND processing_deadline < ?", model.StatusProcessing, now).
		Updates(map[string]interface{}{
			"status":              model.StatusPending,
			"processing_deadline": nil,
		})
	if result.RowsAffected > 0 {
		log.Printf("[scheduler] reset %d stuck tasks", result.RowsAffected)
	}
}
