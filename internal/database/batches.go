package database

import (
	"context"
	"time"

	"matchmind/internal/models"
)

func (s *Store) ListBatches(ctx context.Context, userID int64, limit, offset int) ([]models.BatchSummary, int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ingestion_batches WHERE created_by=$1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,
			LEFT(REGEXP_REPLACE(TRIM(raw_text), E'\\s+', ' ', 'g'), 140),
			source_name,status,total_items,buy_count,sell_count,unknown_count,duplicate_count,
			COALESCE(error_code,''),COALESCE(error_message,''),created_at,completed_at
		FROM ingestion_batches
		WHERE created_by=$1
		ORDER BY created_at DESC,id DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]models.BatchSummary, 0, limit)
	for rows.Next() {
		var item models.BatchSummary
		if err := rows.Scan(&item.ID, &item.RawTextPreview, &item.SourceName, &item.Status,
			&item.TotalItems, &item.BuyCount, &item.SellCount, &item.UnknownCount,
			&item.DuplicateCount, &item.ErrorCode, &item.ErrorMessage,
			&item.CreatedAt, &item.CompletedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) RecoverStaleBatches(ctx context.Context, staleAfter time.Duration) (int64, error) {
	seconds := int64(staleAfter / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE ingestion_batches
		SET status='retry',
			error_code='worker_interrupted',
			error_message='后台处理曾中断，系统正在自动重试',
			started_at=NULL,
			completed_at=NULL
		WHERE status='processing'
			AND started_at < NOW()-($1*INTERVAL '1 second')`, seconds)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
