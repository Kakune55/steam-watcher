package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"steam-watcher/internal/app"
	"steam-watcher/internal/config"
	"steam-watcher/internal/logx"
	"steam-watcher/internal/store"
	"steam-watcher/internal/web"
)

func main() {
	logger := logx.NewLogger()
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DatabasePath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	collector := app.NewCollector(cfg, db)
	server := web.NewServer(cfg, db, collector)

	if cfg.CollectOnStart {
		go func() {
			if _, err := collector.CollectOnce(context.Background()); err != nil {
				slog.Warn("initial collection failed", "error", err)
			}
		}()
	}

	go collector.StartScheduler(context.Background())

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: server,
	}
	httpServer.RegisterOnShutdown(server.NotifyShutdown)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("steam watcher listening", "addr", cfg.ListenAddr, "db", cfg.DatabasePath)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server.NotifyShutdown()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}
