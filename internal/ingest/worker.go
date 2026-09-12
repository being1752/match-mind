package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"matchmind/internal/ai"
	"matchmind/internal/database"
)

type Worker struct {
	store            *database.Store
	ai               *ai.Client
	interval         time.Duration
	embeddingEnabled bool
}

func NewWorker(store *database.Store, client *ai.Client, interval time.Duration, embeddingEnabled bool) *Worker {
	return &Worker{store: store, ai: client, interval: interval, embeddingEnabled: embeddingEnabled}
}

func (w *Worker) Run(ctx context.Context) {
	if recovered, err := w.store.RecoverStaleBatches(ctx, 10*time.Minute); err != nil {
		slog.Error("recover stale ingestion batches", "error", err)
	} else if recovered > 0 {
		slog.Warn("stale ingestion batches queued for retry", "count", recovered)
	}
	if w.embeddingEnabled {
		if err := w.store.QueueMissingEmbeddings(ctx); err != nil {
			slog.Error("queue missing embeddings", "error", err)
		}
	}
	workTicker := time.NewTicker(w.interval)
	recoveryTicker := time.NewTicker(time.Minute)
	defer workTicker.Stop()
	defer recoveryTicker.Stop()
	for {
		if err := w.processOne(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, context.Canceled) {
			slog.Error("process ingestion batch", "error", err)
		}
		if w.embeddingEnabled {
			if err := w.processEmbeddingOne(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, context.Canceled) {
				slog.Error("process embedding job", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-recoveryTicker.C:
			if recovered, err := w.store.RecoverStaleBatches(ctx, 10*time.Minute); err != nil {
				slog.Error("recover stale ingestion batches", "error", err)
			} else if recovered > 0 {
				slog.Warn("stale ingestion batches queued for retry", "count", recovered)
			}
		case <-workTicker.C:
		}
	}
}

func (w *Worker) processEmbeddingOne(ctx context.Context) error {
	job, err := w.store.ClaimEmbeddingJob(ctx)
	if err != nil {
		return err
	}
	content, err := w.store.EmbeddingContent(ctx, job.EntityType, job.EntityID)
	if err != nil {
		_ = w.store.FailEmbeddingJob(context.Background(), job, err.Error())
		return err
	}
	result, err := w.ai.Embed(ctx, []string{content})
	if err != nil {
		_ = w.store.FailEmbeddingJob(context.Background(), job, err.Error())
		return err
	}
	if err := w.store.SaveEmbedding(ctx, job, content, result.ModelName, result.Vectors[0]); err != nil {
		_ = w.store.FailEmbeddingJob(context.Background(), job, err.Error())
		return err
	}
	if err := w.store.PostProcessEmbedding(ctx, job.EntityType, job.EntityID, result.ModelName); err != nil {
		slog.Error("post process embedding", "entity_type", job.EntityType, "entity_id", job.EntityID, "error", err)
	}
	slog.Info("embedding completed", "entity_type", job.EntityType, "entity_id", job.EntityID, "model", result.ModelName)
	return nil
}

func (w *Worker) processOne(ctx context.Context) error {
	batch, err := w.store.ClaimBatch(ctx)
	if err != nil {
		return err
	}
	started := time.Now()
	slog.Info("ingestion batch started", "batch_id", batch.ID, "characters", len([]rune(batch.RawText)))

	response, modelName, err := w.ai.Structure(ctx, batch.RawText)
	if err != nil {
		_ = w.store.FailBatch(context.Background(), batch.ID, "ai_processing_failed", "AI处理服务暂时不可用，请稍后重新处理")
		slog.Error("ingestion batch failed", "batch_id", batch.ID, "duration", time.Since(started), "error", err)
		return err
	}
	if err := w.store.SaveStructuredBatch(ctx, batch.ID, response, modelName); err != nil {
		_ = w.store.FailBatch(context.Background(), batch.ID, "data_save_failed", "数据保存失败，请稍后重新处理")
		slog.Error("ingestion batch save failed", "batch_id", batch.ID, "duration", time.Since(started), "error", err)
		return err
	}
	slog.Info("ingestion batch completed", "batch_id", batch.ID, "items", len(response.Items), "model", modelName, "duration", time.Since(started))
	return nil
}
