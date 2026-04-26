package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/api/router"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/api/ws"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/config"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/db"
	appredis "github.com/Mininglamp-OSS/octo-smart-summary/internal/redis"
)

func main() {
	cfg := config.Load()

	// Init DB
	summaryDB, err := db.New(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("[main] connect summary DB: %v", err)
	}

	// Init Redis
	rdb := appredis.New(cfg.RedisAddr, cfg.RedisDB)
	_ = rdb // used by auth middleware via global or DI in future

	// Init WebSocket hub
	hub := ws.NewHub()

	// Public API server
	publicRouter := router.SetupPublic(summaryDB, hub)
	publicSrv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: publicRouter,
	}

	// Internal callback server (localhost only)
	internalRouter := router.SetupInternal(hub)
	internalSrv := &http.Server{
		Addr:    net.JoinHostPort("127.0.0.1", cfg.APIInternalPort),
		Handler: internalRouter,
	}

	// Start servers
	go func() {
		log.Printf("[api] public server listening on :%s", cfg.APIPort)
		if err := publicSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[api] public server: %v", err)
		}
	}()

	go func() {
		log.Printf("[api] internal server listening on 127.0.0.1:%s", cfg.APIInternalPort)
		if err := internalSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[api] internal server: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Println("[api] shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	publicSrv.Shutdown(ctx)
	internalSrv.Shutdown(ctx)

	if rdb != nil {
		rdb.Close()
	}

	log.Println("[api] exited")
}
