package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type EmbeddingJob struct {
	ID            int64
	EntityType    string
	EntityID      int64
	EmbeddingType string
	RetryCount    int
	MaxRetries    int
}

func (s *Store) QueueMissingEmbeddings(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO embedding_jobs(entity_type,entity_id)
		SELECT 'buy_demand',id FROM buy_demands b
		WHERE status='active' AND NOT EXISTS (
			SELECT 1 FROM entity_embeddings e
			WHERE e.entity_type='buy_demand' AND e.entity_id=b.id AND e.embedding_type='matching_v1'
		)
		ON CONFLICT(entity_type,entity_id,embedding_type)
		DO UPDATE SET status='pending',retry_count=0,error_message=NULL,
			available_at=NOW(),updated_at=NOW()
		WHERE embedding_jobs.status='failed';
		INSERT INTO embedding_jobs(entity_type,entity_id)
		SELECT 'sell_project',id FROM sell_projects s
		WHERE status='active' AND NOT EXISTS (
			SELECT 1 FROM entity_embeddings e
			WHERE e.entity_type='sell_project' AND e.entity_id=s.id AND e.embedding_type='matching_v1'
		)
		ON CONFLICT(entity_type,entity_id,embedding_type)
		DO UPDATE SET status='pending',retry_count=0,error_message=NULL,
			available_at=NOW(),updated_at=NOW()
		WHERE embedding_jobs.status='failed'`)
	return err
}

func queueEmbedding(ctx context.Context, tx pgx.Tx, entityType string, entityID int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO embedding_jobs(entity_type,entity_id)
		VALUES($1,$2)
		ON CONFLICT(entity_type,entity_id,embedding_type)
		DO UPDATE SET status='pending',retry_count=0,error_message=NULL,available_at=NOW(),updated_at=NOW()`,
		entityType, entityID)
	return err
}

func (s *Store) ClaimEmbeddingJob(ctx context.Context) (EmbeddingJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmbeddingJob{}, err
	}
	defer tx.Rollback(ctx)
	var job EmbeddingJob
	err = tx.QueryRow(ctx, `
		SELECT id,entity_type,entity_id,embedding_type,retry_count,max_retries
		FROM embedding_jobs
		WHERE status IN ('pending','retry') AND available_at <= NOW()
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED LIMIT 1`).
		Scan(&job.ID, &job.EntityType, &job.EntityID, &job.EmbeddingType, &job.RetryCount, &job.MaxRetries)
	if err != nil {
		return EmbeddingJob{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE embedding_jobs SET status='processing',started_at=NOW(),updated_at=NOW(),error_message=NULL
		WHERE id=$1`, job.ID); err != nil {
		return EmbeddingJob{}, err
	}
	return job, tx.Commit(ctx)
}

func (s *Store) EmbeddingContent(ctx context.Context, entityType string, entityID int64) (string, error) {
	table, err := entityTable(entityType)
	if err != nil {
		return "", err
	}
	var raw []byte
	query := fmt.Sprintf(`
		SELECT jsonb_strip_nulls(to_jsonb(t) - ARRAY[
			'id','canonical_hash','status','merged_into_id','first_item_id','created_at','updated_at'
		]) FROM %s t WHERE id=$1 AND status='active'`, table)
	if err := s.pool.QueryRow(ctx, query, entityID).Scan(&raw); err != nil {
		return "", err
	}
	var compact any
	if err := json.Unmarshal(raw, &compact); err != nil {
		return "", err
	}
	normalized, _ := json.Marshal(compact)
	label := "买方需求"
	if entityType == "sell_project" {
		label = "卖方项目"
	}
	return label + "：" + string(normalized), nil
}

func (s *Store) SaveEmbedding(ctx context.Context, job EmbeddingJob, content, model string, vector []float32) error {
	if len(vector) != 1024 {
		return fmt.Errorf("expected 1024 dimensions, got %d", len(vector))
	}
	encoded, _ := json.Marshal(vector)
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO entity_embeddings(entity_type,entity_id,embedding_type,model_name,content_text,content_hash,embedding,status)
		VALUES($1,$2,$3,$4,$5,$6,$7::vector,'completed')
		ON CONFLICT(entity_type,entity_id,embedding_type,model_name)
		DO UPDATE SET content_text=EXCLUDED.content_text,content_hash=EXCLUDED.content_hash,
			embedding=EXCLUDED.embedding,status='completed',error_message=NULL,updated_at=NOW()`,
		job.EntityType, job.EntityID, job.EmbeddingType, model, content, hash, string(encoded))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE embedding_jobs SET status='completed',completed_at=NOW(),updated_at=NOW(),error_message=NULL
		WHERE id=$1`, job.ID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailEmbeddingJob(ctx context.Context, job EmbeddingJob, message string) error {
	next := job.RetryCount + 1
	status := "retry"
	if next >= job.MaxRetries {
		status = "failed"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE embedding_jobs SET status=$2,retry_count=$3,error_message=$4,
			available_at=NOW()+($5*INTERVAL '1 second'),completed_at=CASE WHEN $2='failed' THEN NOW() ELSE NULL END,
			updated_at=NOW() WHERE id=$1`,
		job.ID, status, next, message, retryDelay(next))
	return err
}

func retryDelay(attempt int) int {
	delays := []int{5, 15, 60, 300, 900}
	if attempt < 1 {
		return delays[0]
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}

func entityTable(entityType string) (string, error) {
	switch entityType {
	case "buy_demand":
		return "buy_demands", nil
	case "sell_project":
		return "sell_projects", nil
	default:
		return "", errors.New("unsupported entity type")
	}
}

var _ = time.Time{}
