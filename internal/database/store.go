package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"matchmind/internal/models"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateBatch(ctx context.Context, rawText, sourceName, sourceFile, clientRequestID string, createdBy int64) (int64, string, error) {
	if strings.TrimSpace(sourceName) == "" {
		sourceName = "人工录入"
	}
	clientRequestID = strings.TrimSpace(clientRequestID)
	var id int64
	var status string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ingestion_batches(raw_text, source_name, source_file, client_request_id, created_by)
		VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5)
		ON CONFLICT(created_by,client_request_id)
		DO UPDATE SET client_request_id=EXCLUDED.client_request_id
		RETURNING id,status`, rawText, sourceName, sourceFile, clientRequestID, createdBy).Scan(&id, &status)
	return id, status, err
}

func (s *Store) ClaimBatch(ctx context.Context) (*models.Batch, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `
		SELECT id, raw_text, source_name, COALESCE(source_file,''), created_at
		FROM ingestion_batches
		WHERE status IN ('pending','retry')
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED LIMIT 1`)
	batch := &models.Batch{}
	if err := row.Scan(&batch.ID, &batch.RawText, &batch.SourceName, &batch.SourceFile, &batch.CreatedAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ingestion_batches SET status='processing', started_at=NOW(), error_code=NULL, error_message=NULL WHERE id=$1`, batch.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	batch.Status = "processing"
	return batch, nil
}

func (s *Store) SaveStructuredBatch(ctx context.Context, batchID int64, response models.StructuredResponse, modelName string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ownerUserID int64
	if err := tx.QueryRow(ctx, "SELECT COALESCE(created_by,0) FROM ingestion_batches WHERE id=$1", batchID).Scan(&ownerUserID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM ingestion_items WHERE batch_id=$1`, batchID); err != nil {
		return err
	}
	buyCount, sellCount, unknownCount, duplicateCount := 0, 0, 0, 0
	for pos, item := range response.Items {
		if item.ItemIndex <= 0 {
			item.ItemIndex = pos + 1
		}
		if item.RawText == "" {
			continue
		}
		if item.DetectedType != "buy_demand" && item.DetectedType != "sell_project" {
			item.DetectedType = "unknown"
		}
		if len(item.StructuredData) == 0 {
			item.StructuredData = json.RawMessage(`{}`)
		}
		if item.DetectedType != "unknown" && isAggregateItem(item) {
			return fmt.Errorf("refusing aggregate structured item %q: atomic split is required", item.Title)
		}
		var extraction map[string]any
		if json.Unmarshal(item.StructuredData, &extraction) == nil {
			if title, ok := extraction["title"].(string); !ok || strings.TrimSpace(title) == "" {
				extraction["title"] = item.Title
				item.StructuredData, _ = json.Marshal(extraction)
			}
		}

		var itemID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO ingestion_items(batch_id,item_index,raw_text,detected_type,classification_confidence,extraction_json,model_name)
			VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			batchID, item.ItemIndex, item.RawText, item.DetectedType, item.ClassificationConfidence, item.StructuredData, modelName).Scan(&itemID)
		if err != nil {
			return err
		}

		var entityID int64
		var duplicate bool
		switch item.DetectedType {
		case "buy_demand":
			buyCount++
			entityID, duplicate, err = upsertBuyDemand(ctx, tx, itemID, ownerUserID, item)
		case "sell_project":
			sellCount++
			entityID, duplicate, err = upsertSellProject(ctx, tx, itemID, ownerUserID, item)
		default:
			unknownCount++
		}
		if err != nil {
			return err
		}
		if duplicate {
			duplicateCount++
		}
		if entityID > 0 {
			_, err = tx.Exec(ctx, `UPDATE ingestion_items SET entity_id=$1, was_duplicate=$2 WHERE id=$3`, entityID, duplicate, itemID)
			if err != nil {
				return err
			}
			relation := "initial"
			if duplicate {
				relation = "duplicate_source"
			}
			_, err = tx.Exec(ctx, `INSERT INTO entity_sources(entity_type,entity_id,item_id,relation_type) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, item.DetectedType, entityID, itemID, relation)
			if err != nil {
				return err
			}
			if duplicate {
				reason, _ := json.Marshal(map[string]any{"rule": "canonical_hash"})
				_, err = tx.Exec(ctx, `
					INSERT INTO entity_merge_events(
						entity_type,entity_id,source_item_id,action,merge_method,reason_json,operator_id)
					SELECT $1,$2,$3,'exact_duplicate','canonical_hash',$4,created_by
					FROM ingestion_batches WHERE id=$5`,
					item.DetectedType, entityID, itemID, reason, batchID)
				if err != nil {
					return err
				}
			}
			if !duplicate {
				if err := queueEmbedding(ctx, tx, item.DetectedType, entityID); err != nil {
					return err
				}
				snapshot, _ := json.Marshal(map[string]any{
					"title":           item.Title,
					"structured_data": json.RawMessage(item.StructuredData),
				})
				_, err = tx.Exec(ctx, `
					INSERT INTO entity_versions(entity_type,entity_id,version_number,snapshot_json,source_item_id,created_by)
					SELECT $1,$2,1,$3,$4,created_by FROM ingestion_batches WHERE id=$5
					ON CONFLICT DO NOTHING`,
					item.DetectedType, entityID, snapshot, itemID, batchID)
				if err != nil {
					return err
				}
			}
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE ingestion_batches SET status='completed', total_items=$2, buy_count=$3, sell_count=$4,
		unknown_count=$5, duplicate_count=$6, completed_at=NOW() WHERE id=$1`,
		batchID, len(response.Items), buyCount, sellCount, unknownCount, duplicateCount)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func isAggregateItem(item models.StructuredItem) bool {
	title := strings.TrimSpace(item.Title)
	for _, marker := range []string{"多个项目", "多项项目", "若干项目", "一批项目", "多个需求", "多项需求", "若干需求", "一批需求"} {
		if strings.Contains(title, marker) {
			return true
		}
	}
	var data map[string]any
	if json.Unmarshal(item.StructuredData, &data) != nil {
		return false
	}
	for parent, child := range map[string]string{
		"extra_facts":       "sub_projects",
		"extra_constraints": "sub_requirements",
	} {
		container, ok := data[parent].(map[string]any)
		if !ok {
			continue
		}
		values, ok := container[child].([]any)
		if ok && len(values) > 1 {
			return true
		}
	}
	return false
}

func upsertBuyDemand(ctx context.Context, tx pgx.Tx, itemID, ownerUserID int64, item models.StructuredItem) (int64, bool, error) {
	data := map[string]any{}
	if err := json.Unmarshal(item.StructuredData, &data); err != nil {
		return 0, false, err
	}
	hash := canonicalHash(item.RawText)
	var existing int64
	err := tx.QueryRow(ctx, `SELECT id FROM buy_demands WHERE owner_user_id=$1 AND canonical_hash=$2 AND status='active'`, ownerUserID, hash).Scan(&existing)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE buy_demands SET updated_at=NOW() WHERE id=$1`, existing)
		return existing, true, err
	}
	if err != pgx.ErrNoRows {
		return 0, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO buy_demands(title,buyer_name,buyer_type,summary,industries,transaction_types,target_types,
		preferred_regions,preferred_stages,investment_amount_min,investment_amount_max,target_revenue_min,
		target_revenue_max,target_net_profit_min,target_net_profit_max,target_valuation_min,target_valuation_max,
		pe_min,pe_max,listed_status_requirement,control_ratio_min,profitability_required,extra_constraints,
		canonical_hash,first_item_id,owner_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,NULLIF($26,0))
		RETURNING id`,
		fallback(item.Title, stringValue(data, "title"), "未命名买方需求"), nullableString(data, "buyer_name"), nullableString(data, "buyer_type"), nullableString(data, "summary"),
		jsonValue(data, "industries", []any{}), jsonValue(data, "transaction_types", []any{}), jsonValue(data, "target_types", []any{}),
		jsonValue(data, "preferred_regions", []any{}), jsonValue(data, "preferred_stages", []any{}), numberValue(data, "investment_amount_min"), numberValue(data, "investment_amount_max"),
		numberValue(data, "target_revenue_min"), numberValue(data, "target_revenue_max"), numberValue(data, "target_net_profit_min"), numberValue(data, "target_net_profit_max"),
		numberValue(data, "target_valuation_min"), numberValue(data, "target_valuation_max"), numberValue(data, "pe_min"), numberValue(data, "pe_max"),
		nullableString(data, "listed_status_requirement"), numberValue(data, "control_ratio_min"), boolValue(data, "profitability_required"), jsonValue(data, "extra_constraints", map[string]any{}), hash, itemID, ownerUserID).Scan(&id)
	return id, false, err
}

func upsertSellProject(ctx context.Context, tx pgx.Tx, itemID, ownerUserID int64, item models.StructuredItem) (int64, bool, error) {
	data := map[string]any{}
	if err := json.Unmarshal(item.StructuredData, &data); err != nil {
		return 0, false, err
	}
	hash := canonicalHash(item.RawText)
	var existing int64
	err := tx.QueryRow(ctx, `SELECT id FROM sell_projects WHERE owner_user_id=$1 AND canonical_hash=$2 AND status='active'`, ownerUserID, hash).Scan(&existing)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE sell_projects SET updated_at=NOW() WHERE id=$1`, existing)
		return existing, true, err
	}
	if err != pgx.ErrNoRows {
		return 0, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO sell_projects(title,company_name,summary,industries,transaction_types,project_types,financing_round,
		financing_amount_min,financing_amount_max,revenue_min,revenue_max,net_profit_min,net_profit_max,valuation_min,
		valuation_max,pe_min,pe_max,province,city,listed_status,transfer_ratio_min,transfer_ratio_max,extra_facts,
		canonical_hash,first_item_id,owner_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,NULLIF($26,0))
		RETURNING id`,
		fallback(item.Title, stringValue(data, "title"), "未命名卖方项目"), nullableString(data, "company_name"), nullableString(data, "summary"),
		jsonValue(data, "industries", []any{}), jsonValue(data, "transaction_types", []any{}), jsonValue(data, "project_types", []any{}), nullableString(data, "financing_round"),
		numberValue(data, "financing_amount_min"), numberValue(data, "financing_amount_max"), numberValue(data, "revenue_min"), numberValue(data, "revenue_max"),
		numberValue(data, "net_profit_min"), numberValue(data, "net_profit_max"), numberValue(data, "valuation_min"), numberValue(data, "valuation_max"),
		numberValue(data, "pe_min"), numberValue(data, "pe_max"), nullableString(data, "province"), nullableString(data, "city"), nullableString(data, "listed_status"),
		numberValue(data, "transfer_ratio_min"), numberValue(data, "transfer_ratio_max"), jsonValue(data, "extra_facts", map[string]any{}), hash, itemID, ownerUserID).Scan(&id)
	return id, false, err
}

func (s *Store) FailBatch(ctx context.Context, batchID int64, code, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE ingestion_batches SET status='failed', error_code=$2, error_message=$3, completed_at=NOW() WHERE id=$1`, batchID, code, message)
	return err
}

func (s *Store) RetryBatch(ctx context.Context, batchID, userID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE ingestion_batches SET status='retry', error_code=NULL, error_message=NULL, started_at=NULL, completed_at=NULL WHERE id=$1 AND created_by=$2 AND status='failed'`, batchID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) GetBatch(ctx context.Context, id, userID int64) (*models.Batch, error) {
	b := &models.Batch{}
	err := s.pool.QueryRow(ctx, `SELECT id,raw_text,source_name,COALESCE(source_file,''),status,total_items,buy_count,sell_count,unknown_count,duplicate_count,COALESCE(error_code,''),COALESCE(error_message,''),created_at,completed_at FROM ingestion_batches WHERE id=$1 AND created_by=$2`, id, userID).
		Scan(&b.ID, &b.RawText, &b.SourceName, &b.SourceFile, &b.Status, &b.TotalItems, &b.BuyCount, &b.SellCount, &b.UnknownCount, &b.DuplicateCount, &b.ErrorCode, &b.ErrorMessage, &b.CreatedAt, &b.CompletedAt)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id,i.batch_id,i.item_index,i.raw_text,i.detected_type,
			COALESCE(i.classification_confidence,0),i.processing_status,i.extraction_json,
			COALESCE(NULLIF(i.extraction_json->>'title',''),
				CASE i.detected_type
					WHEN 'buy_demand' THEN (SELECT b.title FROM buy_demands b WHERE b.id=i.entity_id)
					WHEN 'sell_project' THEN (SELECT s.title FROM sell_projects s WHERE s.id=i.entity_id)
				END,
				NULLIF(LEFT(SPLIT_PART(TRIM(i.raw_text),E'\n',1),80),''),
				'未命名信息'),
			i.entity_id,i.was_duplicate,i.created_at
		FROM ingestion_items i WHERE i.batch_id=$1 ORDER BY i.item_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item models.IngestionItem
		if err := rows.Scan(&item.ID, &item.BatchID, &item.ItemIndex, &item.RawText, &item.DetectedType, &item.ClassificationConfidence, &item.ProcessingStatus, &item.ExtractionJSON, &item.Title, &item.EntityID, &item.WasDuplicate, &item.CreatedAt); err != nil {
			return nil, err
		}
		b.Items = append(b.Items, item)
	}
	return b, rows.Err()
}

func (s *Store) ListEntities(ctx context.Context, entityType, query string, limit, offset int) ([]json.RawMessage, int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var table string
	if entityType == "buy_demand" {
		table = "buy_demands"
	} else if entityType == "sell_project" {
		table = "sell_projects"
	} else {
		return nil, 0, fmt.Errorf("unsupported entity type")
	}
	pattern := "%" + strings.TrimSpace(query) + "%"
	var total int
	if err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE status='active' AND ($1='' OR title ILIKE $2)`, table), query, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT to_jsonb(t) - 'canonical_hash' FROM %s t WHERE status='active' AND ($1='' OR title ILIKE $2) ORDER BY updated_at DESC LIMIT $3 OFFSET $4`, table), query, pattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		result = append(result, raw)
	}
	return result, total, rows.Err()
}

func canonicalHash(value string) string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("，。；：、,.:;!?！？【】[]()（）\"'", r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}

func fallback(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func stringValue(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
func nullableString(m map[string]any, key string) any {
	if v := strings.TrimSpace(stringValue(m, key)); v != "" {
		return v
	}
	return nil
}
func numberValue(m map[string]any, key string) any {
	switch v := m[key].(type) {
	case float64:
		return v
	case json.Number:
		return v.String()
	case string:
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return nil
}
func boolValue(m map[string]any, key string) any {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return nil
}
func jsonValue(m map[string]any, key string, fallback any) []byte {
	v, ok := m[key]
	if !ok || v == nil {
		v = fallback
	}
	b, _ := json.Marshal(v)
	return b
}
