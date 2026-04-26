package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/config"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/db"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/service"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/worker"
)

func main() {
	cfg := config.Load()

	// Init summary DB
	summaryDB, err := db.New(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("[worker] connect summary DB: %v", err)
	}

	// Init IM DB (read-only, for message fetching)
	imDB, err := db.New(cfg.IMMySQLDSN)
	if err != nil {
		log.Fatalf("[worker] connect IM DB: %v", err)
	}

	// Init LLM client
	llm := service.NewLLMClient(cfg.LLMApiURL, cfg.LLMApiKey, cfg.LLMModel, cfg.LLMTimeout, cfg.LLMMaxToken)

	// Start worker pool
	pool := worker.NewWorkerPool(cfg.WorkerMaxConcurrent)

	// Start processor (polling loop)
	proc := worker.NewProcessor(summaryDB, imDB, pool, llm, cfg)
	go proc.Run()

	// Start scheduler (cron jobs)
	cron := worker.StartScheduler(summaryDB)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Println("[worker] shutting down...")

	proc.Stop()
	pool.Drain()
	cron.Stop()

	log.Println("[worker] exited")
}
