package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/config"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/pipeline"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/service"
	"gorm.io/gorm"
)

// Processor polls the DB for pending tasks and dispatches them to the pool.
type Processor struct {
	db          *gorm.DB
	imDB        *gorm.DB
	pool        *WorkerPool
	llm         *service.LLMClient
	cfg         *config.Config
	stopCh      chan struct{}
}

// NewProcessor creates a new task processor.
func NewProcessor(db, imDB *gorm.DB, pool *WorkerPool, llm *service.LLMClient, cfg *config.Config) *Processor {
	return &Processor{
		db:     db,
		imDB:   imDB,
		pool:   pool,
		llm:    llm,
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Run starts the polling loop. Call Stop() to exit.
func (p *Processor) Run() {
	interval := time.Duration(p.cfg.WorkerPollInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[processor] polling every %v", interval)
	for {
		select {
		case <-p.stopCh:
			log.Println("[processor] stopping")
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

// Stop signals the processor to stop polling.
func (p *Processor) Stop() {
	close(p.stopCh)
}

func (p *Processor) poll() {
	var tasks []model.SummaryTask
	now := time.Now().UTC()

	err := p.db.Where(
		"status = ? AND retry_count < ? AND (processing_deadline IS NULL OR processing_deadline < ?)",
		model.StatusPending, p.cfg.WorkerMaxRetry, now,
	).Limit(10).Find(&tasks).Error
	if err != nil {
		log.Printf("[processor] query pending tasks: %v", err)
		return
	}

	for _, task := range tasks {
		task := task
		// Optimistic lock: update status to Processing
		deadline := now.Add(time.Duration(p.cfg.WorkerLeaseMinutes) * time.Minute)
		result := p.db.Model(&model.SummaryTask{}).
			Where("id = ? AND status = ?", task.ID, model.StatusPending).
			Updates(map[string]interface{}{
				"status":              model.StatusProcessing,
				"processing_deadline": deadline,
			})
		if result.RowsAffected == 0 {
			continue // another worker grabbed it
		}

		p.pool.Submit(func() {
			p.processTask(task)
		})
	}
}

func (p *Processor) processTask(task model.SummaryTask) {
	log.Printf("[processor] processing task %d (%s)", task.ID, task.TaskNo)

	// Send progress callback
	p.sendCallback(model.TaskEvent{
		TaskID:   task.ID,
		Status:   model.StatusProcessing,
		Progress: 10,
		Message:  "开始处理",
	})

	err := p.executePipeline(task)
	if err != nil {
		log.Printf("[processor] task %d failed: %v", task.ID, err)
		errMsg := err.Error()
		newRetry := task.RetryCount + 1
		newStatus := model.StatusPending
		if newRetry >= p.cfg.WorkerMaxRetry {
			newStatus = model.StatusFailed
		}
		p.db.Model(&model.SummaryTask{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
			"status":              newStatus,
			"retry_count":         newRetry,
			"error_message":       errMsg,
			"processing_deadline": nil,
		})
		p.sendCallback(model.TaskEvent{
			TaskID:  task.ID,
			Status:  newStatus,
			Message: errMsg,
		})
		return
	}

	// Success
	p.db.Model(&model.SummaryTask{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
		"status":              model.StatusCompleted,
		"processing_deadline": nil,
	})
	p.sendCallback(model.TaskEvent{
		TaskID:   task.ID,
		Status:   model.StatusCompleted,
		Progress: 100,
		Message:  "总结完成",
	})
	log.Printf("[processor] task %d completed", task.ID)
}

func (p *Processor) executePipeline(task model.SummaryTask) error {
	ctx := context.Background()

	// Load sources
	var sources []model.SummarySource
	if err := p.db.Where("task_id = ?", task.ID).Find(&sources).Error; err != nil {
		return fmt.Errorf("load sources: %w", err)
	}

	// Build specified sources for pipeline
	specifiedSources := make([]map[string]interface{}, 0, len(sources))
	for _, s := range sources {
		specifiedSources = append(specifiedSources, map[string]interface{}{
			"source_id":   s.SourceID,
			"source_type": s.SourceType,
			"source_name": s.SourceName,
		})
	}

	// Fetch messages via 4-layer pipeline
	llmFn := func(ctx context.Context, prompt string) (string, error) {
		return p.llm.CallRaw(ctx, prompt)
	}
	messages, err := pipeline.ResolveAndFetchMessages(
		ctx, task.CreatorID, specifiedSources, task.Title,
		task.TimeRangeStart, task.TimeRangeEnd,
		p.imDB, llmFn, p.cfg.MsgTableCount,
	)
	if err != nil {
		return fmt.Errorf("fetch messages: %w", err)
	}

	if len(messages) == 0 {
		// No messages, create empty result
		nextVer, _ := service.GetNextVersion(p.db, task.ID)
		now := time.Now().UTC()
		result := model.SummaryResult{
			TaskID:        task.ID,
			Content:       "该时段内无文本消息",
			TotalMsgCount: 0,
			ModelVersion:  p.llm.ModelVersion(),
			Version:       nextVer,
			GeneratedAt:   now,
		}
		return p.db.Create(&result).Error
	}

	p.sendCallback(model.TaskEvent{
		TaskID:   task.ID,
		Status:   model.StatusProcessing,
		Progress: 30,
		Message:  fmt.Sprintf("获取到 %d 条消息，开始总结", len(messages)),
	})

	// Convert messages to map format for chunking
	msgMaps := make([]map[string]interface{}, len(messages))
	for i, m := range messages {
		msgMaps[i] = map[string]interface{}{
			"sender_uid":  m.SenderUID,
			"content":     m.Content,
			"send_time":   m.SendTime,
			"source_name": m.SourceName,
		}
	}

	chunks := service.SplitIntoChunks(msgMaps, 500)

	// Map phase
	var chunkSummaries []string
	var totalTokens int
	startTime := task.TimeRangeStart.Format("2006-01-02 15:04")
	endTime := task.TimeRangeEnd.Format("2006-01-02 15:04")
	sourceName := "多来源"
	if len(sources) == 1 {
		sourceName = sources[0].SourceName
	}

	for i, chunk := range chunks {
		var formatted []string
		for _, msg := range chunk {
			formatted = append(formatted, fmt.Sprintf("[%s] %s: %s",
				msg["send_time"], msg["sender_uid"], msg["content"]))
		}

		summary, tokens, err := p.llm.CallMap(ctx,
			joinStrings(formatted), sourceName, i, len(chunk),
			startTime, endTime,
		)
		if err != nil {
			return fmt.Errorf("map chunk %d: %w", i, err)
		}

		chunkSummaries = append(chunkSummaries, summary)
		totalTokens += tokens

		// Save chunk to DB
		chunkRecord := model.SummaryChunk{
			TaskID:       task.ID,
			ChunkIndex:   i,
			MsgCount:     len(chunk),
			ChunkSummary: summary,
			TokenUsed:    tokens,
			Status:       1,
		}
		if len(sources) > 0 {
			chunkRecord.SummarySourceID = &sources[0].ID
		}
		p.db.Create(&chunkRecord)

		progress := 30 + (60 * (i + 1) / len(chunks))
		p.sendCallback(model.TaskEvent{
			TaskID:   task.ID,
			Status:   model.StatusProcessing,
			Progress: progress,
			Message:  fmt.Sprintf("Map 阶段 %d/%d 完成", i+1, len(chunks)),
		})
	}

	// Reduce phase
	finalContent, reduceTokens, err := p.llm.CallReduce(ctx,
		chunkSummaries, sourceName, startTime, endTime, len(messages),
	)
	if err != nil {
		return fmt.Errorf("reduce: %w", err)
	}
	totalTokens += reduceTokens

	// Save result
	nextVer, _ := service.GetNextVersion(p.db, task.ID)
	now := time.Now().UTC()
	result := model.SummaryResult{
		TaskID:         task.ID,
		Content:        finalContent,
		TotalMsgCount:  len(messages),
		TotalTokenUsed: totalTokens,
		ModelVersion:   p.llm.ModelVersion(),
		Version:        nextVer,
		GeneratedAt:    now,
	}
	return p.db.Create(&result).Error
}

func (p *Processor) sendCallback(event model.TaskEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		log.Printf("[processor] marshal callback: %v", err)
		return
	}

	resp, err := http.Post(p.cfg.WorkerCallbackURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[processor] callback POST failed: %v", err)
		// Fallback: save event to DB
		p.db.Create(&model.SummaryEvent{
			TaskID:   event.TaskID,
			Status:   event.Status,
			Progress: event.Progress,
			Message:  event.Message,
		})
		return
	}
	resp.Body.Close()
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += "\n"
		}
		result += s
	}
	return result
}
