# 续写任务

项目在 /tmp/smart-summary-go/，已有18个Go文件（不要重写已有文件）。

需要补写以下缺失文件：

## 1. cmd/summary-api/main.go

启动两个 HTTP server：
- 公共 API server 监听 :8080（调用 router.SetupPublic）
- 内部 callback server 监听 127.0.0.1:8081（调用 router.SetupInternal）
- 优雅退出（os.Signal SIGTERM/SIGINT）
- 加载 config.Load()，初始化 DB、Redis、WebSocket hub

## 2. cmd/summary-worker/main.go

启动 worker 进程：
- 加载 config，初始化 DB、Redis
- 启动 WorkerPool（maxConcurrent 来自 config）
- 启动 Processor goroutine（轮询 DB 拉任务）
- 启动 Scheduler（robfig/cron，3个扫描循环）
- 优雅退出（SIGTERM/SIGINT → pool.Drain() → cron.Stop()）

## 3. internal/worker/pool.go

```
package worker

import "sync"

type WorkerPool struct {
    sem chan struct{}
    wg  sync.WaitGroup
}

func NewWorkerPool(maxConcurrent int) *WorkerPool {
    return &WorkerPool{sem: make(chan struct{}, maxConcurrent)}
}

// Submit runs fn in a goroutine, blocking if pool is full
func (p *WorkerPool) Submit(fn func()) {
    p.sem <- struct{}{}
    p.wg.Add(1)
    go func() {
        defer func() {
            <-p.sem
            p.wg.Done()
        }()
        fn()
    }()
}

// Drain waits for all submitted tasks to finish
func (p *WorkerPool) Drain() {
    p.wg.Wait()
}
```

## 4. internal/worker/processor.go

Processor polls the DB for PENDING tasks and processes them:
- Every WORKER_POLL_INTERVAL_SECONDS, query: status=0 AND retry_count < max_retry AND (processing_deadline IS NULL OR processing_deadline < NOW())
- Lock task: UPDATE status=2, processing_deadline=NOW()+WORKER_TASK_LEASE_MINUTES WHERE id=? AND status=0 (optimistic locking)
- Submit to WorkerPool:
  - Call service to execute task (LLM MapReduce pipeline)
  - On success: update status=3, call HTTP callback to API
  - On failure: retry_count++, status=0 (or 4 if retry_count >= max_retry)
  - Always send HTTP POST to WORKER_API_CALLBACK_URL with TaskEvent JSON

## 5. internal/worker/scheduler.go

Three cron jobs (every 60s):
- scanPendingSchedules: query summary_schedule where is_active=1 AND next_run_at <= NOW(), create summary_task, update last_run_at+next_run_at
- scanConfirmTimeouts: query summary_task where status=1 AND confirm_deadline < NOW(), update status=5 (CANCELLED). NOTE: do NOT check participant threshold (Python原型的50%门槛不迁移)
- scanStuckTasks: query summary_task where status=2 AND processing_deadline < NOW(), reset status=0, clear processing_deadline

Use github.com/robfig/cron/v3.

## 6. migrations/001_init.sql

Complete P1 MySQL schema from /tmp/smart-summary/docs/P1-plan.md section 4.2.
All 7 tables: summary_task, summary_source, summary_participant, summary_chunk, summary_result, summary_schedule, summary_event.
Include: ENGINE=InnoDB, CHARSET=utf8mb4, COLLATE=utf8mb4_unicode_ci, all indexes.
Note: summary_chunk must have summary_source_id column (was missing in earlier draft).

## 7. Makefile

```makefile
.PHONY: build test docker-build run-api run-worker

build:
	go build ./...

test:
	go test -v -count=1 ./...

docker-build:
	docker build -f Dockerfile.api -t smart-summary-api:local .
	docker build -f Dockerfile.worker -t smart-summary-worker:local .

run-api:
	go run ./cmd/summary-api

run-worker:
	go run ./cmd/summary-worker
```

## 8. Dockerfile.api

Multi-stage build with golang:1.21-alpine and alpine:3.19.
Build: go build -o /bin/summary-api ./cmd/summary-api
Expose 8080 and 8081.

## 9. Dockerfile.worker

Same pattern, build summary-worker binary.

## 10. docker-compose.yml

Two services (summary-api and summary-worker) with shared environment variables for MySQL, Redis, LLM, DMWork.

## 11. Test files (unit tests, no MySQL required)

### internal/pipeline/shard_test.go
Test that CRC32 table selection matches Python: crc32.ChecksumIEEE([]byte(channelID)) % uint32(tableCount)
Test cases: table_count=5, various channel_ids, verify table names

### internal/pipeline/payload_test.go
Test ExtractText:
- raw JSON with type=1: should return text
- raw JSON with type=2: should return empty (non-text)
- base64 encoded JSON with type=1: should return text
- nil payload: should return empty

### internal/middleware/space_test.go
Test SpaceMiddleware using httptest:
- Request with X-Space-Id header: should set space_id in context, return 200
- Request without X-Space-Id: should return 400

### internal/worker/pool_test.go
Test WorkerPool:
- Submit tasks up to max concurrent, verify they run
- Submit more than max, verify blocking behavior
- After all submitted, Drain should return when done

## Final Steps

After writing all files:
1. Run: go mod tidy
2. Run: go build ./...  (must succeed)
3. Run: go vet ./...  (must have no warnings)
4. Run: go test ./internal/pipeline/... ./internal/middleware/... ./internal/worker/... -v  (unit tests must pass, skip DB-dependent tests with t.Skip)
5. Fix any compilation or test errors
6. git add -A && git commit -m "feat: complete P1 Go implementation with tests"
7. Run: openclaw system event --text "P1 Go 实现完成：所有测试通过" --mode now
