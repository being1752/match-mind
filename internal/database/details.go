package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Store) GetEntityDetail(ctx context.Context, entityType string, entityID int64) (map[string]any, error) {
	table, err := entityTable(entityType)
	if err != nil {
		return nil, err
	}
	var entityJSON []byte
	if err := s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT to_jsonb(t)-'canonical_hash' FROM %s t WHERE id=$1`, table),
		entityID).Scan(&entityJSON); err != nil {
		return nil, err
	}
	var entity map[string]any
	if err := json.Unmarshal(entityJSON, &entity); err != nil {
		return nil, err
	}
	sources, err := s.entitySources(ctx, entityType, entityID)
	if err != nil {
		return nil, err
	}
	versions, err := s.entityJSONRows(ctx, `
		SELECT jsonb_build_object(
			'id',v.id,'version_number',v.version_number,'changed_fields',v.changed_fields,
			'snapshot',v.snapshot_json,'created_at',v.created_at,
			'created_by',COALESCE(u.display_name,u.username))
		FROM entity_versions v LEFT JOIN users u ON u.id=v.created_by
		WHERE v.entity_type=$1 AND v.entity_id=$2 ORDER BY v.version_number DESC`,
		entityType, entityID)
	if err != nil {
		return nil, err
	}
	events, err := s.entityJSONRows(ctx, `
		SELECT jsonb_build_object(
			'id',e.id,'action',e.action,'merge_method',e.merge_method,
			'similarity_score',e.similarity_score,'reason',e.reason_json,
			'operator',COALESCE(u.display_name,u.username),'created_at',e.created_at,
			'reverted_at',e.reverted_at)
		FROM entity_merge_events e LEFT JOIN users u ON u.id=e.operator_id
		WHERE e.entity_type=$1 AND e.entity_id=$2 ORDER BY e.created_at DESC`,
		entityType, entityID)
	if err != nil {
		return nil, err
	}
	embedding := map[string]any{"status": "missing"}
	var embeddingStatus, model string
	var updated any
	err = s.pool.QueryRow(ctx, `
		SELECT status,model_name,updated_at FROM entity_embeddings
		WHERE entity_type=$1 AND entity_id=$2 AND embedding_type='matching_v1'
		ORDER BY updated_at DESC LIMIT 1`, entityType, entityID).
		Scan(&embeddingStatus, &model, &updated)
	if err == nil {
		embedding = map[string]any{"status": embeddingStatus, "model_name": model, "updated_at": updated}
	} else if err != pgx.ErrNoRows {
		return nil, err
	} else {
		var jobStatus, jobError string
		if jobErr := s.pool.QueryRow(ctx, `
			SELECT status,COALESCE(error_message,'') FROM embedding_jobs
			WHERE entity_type=$1 AND entity_id=$2 AND embedding_type='matching_v1'`,
			entityType, entityID).Scan(&jobStatus, &jobError); jobErr == nil {
			embedding = map[string]any{"status": jobStatus, "error_message": jobError}
		}
	}
	matches, err := s.ListMatches(ctx, entityType, entityID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"entity_type":  entityType,
		"entity":       entity,
		"sources":      sources,
		"versions":     versions,
		"merge_events": events,
		"embedding":    embedding,
		"matches":      matches,
	}, nil
}

func (s *Store) entitySources(ctx context.Context, entityType string, entityID int64) ([]json.RawMessage, error) {
	return s.entityJSONRows(ctx, `
		SELECT jsonb_build_object(
			'id',es.id,'relation_type',es.relation_type,'raw_text',i.raw_text,
			'extraction_json',i.extraction_json,'uploaded_at',b.created_at,
			'uploader_id',u.id,'uploader',COALESCE(u.display_name,u.username,b.source_name),
			'batch_id',b.id,'item_id',i.id)
		FROM entity_sources es JOIN ingestion_items i ON i.id=es.item_id
		JOIN ingestion_batches b ON b.id=i.batch_id LEFT JOIN users u ON u.id=b.created_by
		WHERE es.entity_type=$1 AND es.entity_id=$2 ORDER BY b.created_at`,
		entityType, entityID)
}

func (s *Store) entityJSONRows(ctx context.Context, query string, args ...any) ([]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		result = append(result, raw)
	}
	return result, rows.Err()
}
