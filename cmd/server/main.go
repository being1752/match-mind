package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"matchmind/internal/ai"
	"matchmind/internal/api"
	"matchmind/internal/auth"
	"matchmind/internal/config"
	"matchmind/internal/database"
	"matchmind/internal/ingest"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		slog.Error("run migrations", "error", err)
		os.Exit(1)
	}

	store := database.NewStore(pool)
	if err := store.EnsureTestUser(ctx, cfg.TestUsername, cfg.TestPassword, cfg.TestDisplayName); err != nil {
		slog.Error("ensure test account", "error", err)
		os.Exit(1)
	}
	aiClient := ai.NewClient(cfg.AIServiceURL, cfg.AIRequestTimeout)
	worker := ingest.NewWorker(store, aiClient, cfg.WorkerPollInterval, cfg.EmbeddingEnabled)
	go worker.Run(ctx)

	handler := api.NewHandler(store, cfg.FrontendOrigin, auth.NewManager(cfg.JWTSecret, cfg.JWTDuration))
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("API listening", "address", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server stopped", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown", "error", err)
	}
}
