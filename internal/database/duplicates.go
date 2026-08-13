package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type DuplicateCandidate struct {
	ID                int64           `json:"id"`
	SourceEntityType  string          `json:"source_entity_type"`
	SourceEntityID    int64           `json:"source_entity_id"`
	CandidateEntityID int64           `json:"candidate_entity_id"`
	SourceTitle       string          `json:"source_title"`
	CandidateTitle    string          `json:"candidate_title"`
	VectorScore       float64         `json:"vector_score"`
	FinalScore        float64         `json:"final_score"`
	SameUser          bool            `json:"same_user"`
	Status            string          `json:"status"`
	ReasonJSON        json.RawMessage `json:"reason_json"`
}

func (s *Store) EvaluateDuplicate(ctx context.Context, entityType string, entityID int64, model string) error {
	table, err := entityTable(entityType)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		WITH source AS (
			SELECT embedding FROM entity_embeddings
			WHERE entity_type=$1 AND entity_id=$2 AND embedding_type='matching_v1' AND model_name=$3
		), source_user AS (
			SELECT b.created_by
			FROM entity_sources es JOIN ingestion_items i ON i.id=es.item_id
			JOIN ingestion_batches b ON b.id=i.batch_id
			WHERE es.entity_type=$1 AND es.entity_id=$2 AND b.created_by IS NOT NULL
			ORDER BY b.created_at LIMIT 1
		)
		SELECT e.entity_id,1-(e.embedding <=> source.embedding) AS score,
			COALESCE((SELECT created_by FROM source_user),0),
			COALESCE((SELECT b.created_by FROM entity_sources es
				JOIN ingestion_items i ON i.id=es.item_id JOIN ingestion_batches b ON b.id=i.batch_id
				WHERE es.entity_type=$1 AND es.entity_id=e.entity_id AND b.created_by IS NOT NULL
				ORDER BY b.created_at LIMIT 1),0)
		FROM entity_embeddings e CROSS JOIN source JOIN %s t ON t.id=e.entity_id
		WHERE e.entity_type=$1 AND e.entity_id<>$2 AND e.embedding_type='matching_v1'
			AND e.model_name=$3 AND e.status='completed' AND t.status='active'
		ORDER BY e.embedding <=> source.embedding LIMIT 1`, table)
	var candidateID, sourceUser, candidateUser int64
	var vectorScore float64
	err = s.pool.QueryRow(ctx, query, entityType, entityID, model).
		Scan(&candidateID, &vectorScore, &sourceUser, &candidateUser)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	conflict, err := s.identityConflict(ctx, entityType, entityID, candidateID)
	if err != nil {
		return err
	}
	sameUser := sourceUser > 0 && sourceUser == candidateUser
	supplement, err := s.supplementCompatible(ctx, entityType, entityID, candidateID)
	if err != nil {
		return err
	}
	if vectorScore < 0.82 && !supplement {
		return nil
	}
	status := "pending"
	rule := "vector_v1"
	if supplement {
		rule = "core_fields_containment_v1"
	}
	reason := map[string]any{
		"same_user":         sameUser,
		"identity_conflict": conflict,
		"supplement":        supplement,
		"rule":              rule,
	}
	reasonJSON, _ := json.Marshal(reason)
	var candidateRecordID int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO duplicate_candidates(
			source_entity_type,source_entity_id,candidate_entity_type,candidate_entity_id,
			source_user_id,candidate_user_id,vector_score,structured_score,final_score,reason_json,status)
		VALUES($1,$2,$1,$3,NULLIF($4,0),NULLIF($5,0),$6,0,$6,$7,$8)
		ON CONFLICT(source_entity_type,source_entity_id,candidate_entity_type,candidate_entity_id)
		DO UPDATE SET vector_score=EXCLUDED.vector_score,final_score=EXCLUDED.final_score,
			reason_json=EXCLUDED.reason_json
		RETURNING id`,
		entityType, entityID, candidateID, sourceUser, candidateUser, vectorScore, reasonJSON, status).
		Scan(&candidateRecordID)
	if err != nil {
		return err
	}
	if sameUser && !conflict && (vectorScore >= 0.94 || supplement) {
		return s.MergeEntities(ctx, candidateRecordID, candidateUser)
	}
	return nil
}

func (s *Store) supplementCompatible(ctx context.Context, entityType string, leftID, rightID int64) (bool, error) {
	table, err := entityTable(entityType)
	if err != nil {
		return false, err
	}
	var leftJSON, rightJSON []byte
	query := fmt.Sprintf(`SELECT to_jsonb(a),to_jsonb(b) FROM %s a,%s b WHERE a.id=$1 AND b.id=$2`, table, table)
	if err := s.pool.QueryRow(ctx, query, leftID, rightID).Scan(&leftJSON, &rightJSON); err != nil {
		return false, err
	}
	var left, right map[string]any
	if err := json.Unmarshal(leftJSON, &left); err != nil {
		return false, err
	}
	if err := json.Unmarshal(rightJSON, &right); err != nil {
		return false, err
	}
	return coreFieldsContainment(entityType, left, right), nil
}

// coreFieldsContainment deliberately ignores summaries and extra JSON fields:
// those are where a later message normally adds supplementary information.
// It auto-merges only when several stable business fields agree and none conflict.
func coreFieldsContainment(entityType string, left, right map[string]any) bool {
	if separatelyNumberedSeries(left, right) {
		return false
	}
	arrayFields := []string{"industries", "transaction_types"}
	scalarFields := []string{"pe_min", "pe_max"}
	if entityType == "buy_demand" {
		arrayFields = append(arrayFields, "target_types", "preferred_regions", "preferred_stages")
		scalarFields = append(scalarFields, "investment_amount_min", "investment_amount_max",
			"target_revenue_min", "target_revenue_max", "target_net_profit_min", "target_net_profit_max",
			"target_valuation_min", "target_valuation_max", "listed_status_requirement", "control_ratio_min")
	} else {
		arrayFields = append(arrayFields, "project_types")
		scalarFields = append(scalarFields, "financing_round", "financing_amount_min", "financing_amount_max",
			"revenue_min", "revenue_max", "net_profit_min", "net_profit_max", "valuation_min", "valuation_max",
			"province", "city", "listed_status", "transfer_ratio_min", "transfer_ratio_max")
	}
	shared, anchors := 0, 0
	for _, field := range arrayFields {
		leftValues, rightValues := stringArray(left[field]), stringArray(right[field])
		if len(leftValues) == 0 || len(rightValues) == 0 {
			continue
		}
		if !anyContains(leftValues, rightValues) {
			return false
		}
		shared++
		if field == "transaction_types" || field == "target_types" || field == "project_types" {
			anchors++
		}
	}
	for _, field := range scalarFields {
		leftValue, leftOK := comparableValue(left[field])
		rightValue, rightOK := comparableValue(right[field])
		if !leftOK || !rightOK {
			continue
		}
		if leftValue != rightValue {
			return false
		}
		shared++
		if field == "listed_status_requirement" || field == "listed_status" || field == "control_ratio_min" || field == "transfer_ratio_min" {
			anchors++
		}
	}
	return shared >= 4 && anchors >= 2
}

func separatelyNumberedSeries(left, right map[string]any) bool {
	leftTitle, _ := left["title"].(string)
	rightTitle, _ := right["title"].(string)
	leftBase, leftMarker := seriesTitle(leftTitle)
	rightBase, rightMarker := seriesTitle(rightTitle)
	return leftBase != "" && leftBase == rightBase && leftMarker != "" && rightMarker != "" && leftMarker != rightMarker
}

func seriesTitle(value string) (string, string) {
	value = strings.TrimSpace(value)
	pairs := [][2]string{{"（一）", "一"}, {"（二）", "二"}, {"（三）", "三"}, {"(一)", "一"}, {"(二)", "二"}, {"(三)", "三"}}
	for _, pair := range pairs {
		if index := strings.Index(value, pair[0]); index >= 0 {
			return normalizeIdentity(value[:index]), pair[1]
		}
	}
	return "", ""
}

func comparableValue(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		normalized := normalizeIdentity(typed)
		return normalized, normalized != ""
	case float64:
		return fmt.Sprintf("%.6f", typed), true
	case json.Number:
		return typed.String(), true
	default:
		return fmt.Sprint(typed), true
	}
}

func (s *Store) identityConflict(ctx context.Context, entityType string, leftID, rightID int64) (bool, error) {
	table, _ := entityTable(entityType)
	field := "buyer_name"
	if entityType == "sell_project" {
		field = "company_name"
	}
	query := fmt.Sprintf(`SELECT COALESCE(a.%s,''),COALESCE(b.%s,'') FROM %s a,%s b WHERE a.id=$1 AND b.id=$2`,
		field, field, table, table)
	var left, right string
	if err := s.pool.QueryRow(ctx, query, leftID, rightID).Scan(&left, &right); err != nil {
		return false, err
	}
	left = normalizeIdentity(left)
	right = normalizeIdentity(right)
	return left != "" && right != "" && left != right, nil
}

func normalizeIdentity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func (s *Store) ListDuplicateCandidates(ctx context.Context) ([]DuplicateCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id,d.source_entity_type,d.source_entity_id,d.candidate_entity_id,
			CASE WHEN d.source_entity_type='buy_demand' THEN sb.title ELSE ss.title END,
			CASE WHEN d.source_entity_type='buy_demand' THEN cb.title ELSE cs.title END,
			COALESCE(d.vector_score,0),d.final_score,
			COALESCE(d.source_user_id=d.candidate_user_id,false),d.status,d.reason_json
		FROM duplicate_candidates d
		LEFT JOIN buy_demands sb ON d.source_entity_type='buy_demand' AND sb.id=d.source_entity_id
		LEFT JOIN buy_demands cb ON d.source_entity_type='buy_demand' AND cb.id=d.candidate_entity_id
		LEFT JOIN sell_projects ss ON d.source_entity_type='sell_project' AND ss.id=d.source_entity_id
		LEFT JOIN sell_projects cs ON d.source_entity_type='sell_project' AND cs.id=d.candidate_entity_id
		WHERE d.status='pending' ORDER BY d.final_score DESC,d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DuplicateCandidate{}
	for rows.Next() {
		var item DuplicateCandidate
		if err := rows.Scan(&item.ID, &item.SourceEntityType, &item.SourceEntityID, &item.CandidateEntityID,
			&item.SourceTitle, &item.CandidateTitle, &item.VectorScore, &item.FinalScore,
			&item.SameUser, &item.Status, &item.ReasonJSON); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) RejectDuplicate(ctx context.Context, id, userID int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE duplicate_candidates SET status='rejected',resolved_by=$2,resolved_at=NOW()
		WHERE id=$1 AND status='pending'`, id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) MergeEntities(ctx context.Context, candidateID, operatorID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var entityType string
	var sourceID, targetID int64
	var score float64
	var sameUser bool
	var candidateReasonJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT source_entity_type,source_entity_id,candidate_entity_id,final_score,
			COALESCE(source_user_id=candidate_user_id,false),reason_json
		FROM duplicate_candidates WHERE id=$1 AND status='pending' FOR UPDATE`, candidateID).
		Scan(&entityType, &sourceID, &targetID, &score, &sameUser, &candidateReasonJSON)
	if err != nil {
		return err
	}
	if sourceID == targetID {
		return fmt.Errorf("cannot merge entity into itself")
	}
	table, _ := entityTable(entityType)
	var targetBefore, sourceBefore []byte
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT to_jsonb(t) FROM %s t WHERE id=$1`, table), targetID).Scan(&targetBefore); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT to_jsonb(t) FROM %s t WHERE id=$1`, table), sourceID).Scan(&sourceBefore); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT item_id FROM entity_sources WHERE entity_type=$1 AND entity_id=$2
		ORDER BY created_at`, entityType, sourceID)
	if err != nil {
		return err
	}
	sourceItemIDs := []int64{}
	for rows.Next() {
		var itemID int64
		if err := rows.Scan(&itemID); err != nil {
			rows.Close()
			return err
		}
		sourceItemIDs = append(sourceItemIDs, itemID)
	}
	rows.Close()
	if len(sourceItemIDs) == 0 {
		return fmt.Errorf("source entity has no source items")
	}
	if err := mergeStructuredEntity(ctx, tx, entityType, targetID, sourceID); err != nil {
		return err
	}
	sourceItemID := sourceItemIDs[0]
	_, err = tx.Exec(ctx, `
		INSERT INTO entity_sources(entity_type,entity_id,item_id,relation_type)
		SELECT entity_type,$3,item_id,'supplement' FROM entity_sources
		WHERE entity_type=$1 AND entity_id=$2
		ON CONFLICT(entity_type,entity_id,item_id)
		DO UPDATE SET relation_type='supplement'`, entityType, sourceID, targetID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE ingestion_items SET entity_id=$3,was_duplicate=true
		WHERE detected_type=$1 AND entity_id=$2`, entityType, sourceID, targetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM entity_sources WHERE entity_type=$1 AND entity_id=$2`, entityType, sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET status='merged',merged_into_id=$2,updated_at=NOW() WHERE id=$1`, table),
		sourceID, targetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM entity_embeddings WHERE entity_type=$1 AND entity_id IN ($2,$3)`,
		entityType, sourceID, targetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM embedding_jobs WHERE entity_type=$1 AND entity_id=$2`,
		entityType, sourceID); err != nil {
		return err
	}
	if err = queueEmbedding(ctx, tx, entityType, targetID); err != nil {
		return err
	}
	var snapshot []byte
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT to_jsonb(t) FROM %s t WHERE id=$1`, table), targetID).Scan(&snapshot); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_number),0)+1 FROM entity_versions
		WHERE entity_type=$1 AND entity_id=$2`, entityType, targetID).Scan(&version); err != nil {
		return err
	}
	changed, _ := json.Marshal([]string{"merged_sources", "structured_fields"})
	if _, err = tx.Exec(ctx, `
		INSERT INTO entity_versions(entity_type,entity_id,version_number,snapshot_json,changed_fields,source_item_id,created_by)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0))`,
		entityType, targetID, version, snapshot, changed, sourceItemID, operatorID); err != nil {
		return err
	}
	method := "manual"
	var candidateReason map[string]any
	_ = json.Unmarshal(candidateReasonJSON, &candidateReason)
	if sameUser && candidateReason["supplement"] == true {
		method = "structured_supplement"
	} else if sameUser && score >= 0.94 {
		method = "same_user_high_confidence"
	}
	reason, _ := json.Marshal(map[string]any{
		"candidate_id":     candidateID,
		"source_entity_id": sourceID,
		"source_item_ids":  sourceItemIDs,
		"target_before":    json.RawMessage(targetBefore),
		"source_before":    json.RawMessage(sourceBefore),
	})
	if _, err = tx.Exec(ctx, `
		INSERT INTO entity_merge_events(entity_type,entity_id,source_entity_id,source_item_id,action,merge_method,similarity_score,reason_json,operator_id)
		VALUES($1,$2,$3,$4,'merge',$5,$6,$7,NULLIF($8,0))`,
		entityType, targetID, sourceID, sourceItemID, method, score, reason, operatorID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE duplicate_candidates SET status='merged',resolved_by=NULLIF($2,0),resolved_at=NOW()
		WHERE id=$1`, candidateID, operatorID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE duplicate_candidates SET status='superseded',resolved_at=NOW()
		WHERE status='pending' AND source_entity_type=$2 AND source_entity_id=$3 AND id<>$1`,
		candidateID, entityType, sourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mergeStructuredEntity(ctx context.Context, tx pgx.Tx, entityType string, targetID, sourceID int64) error {
	var query string
	if entityType == "buy_demand" {
		query = `
			UPDATE buy_demands t SET
				buyer_name=COALESCE(t.buyer_name,s.buyer_name),buyer_type=COALESCE(t.buyer_type,s.buyer_type),
				summary=COALESCE(t.summary,s.summary),
				industries=jsonb_array_union(t.industries,s.industries),
				transaction_types=jsonb_array_union(t.transaction_types,s.transaction_types),
				target_types=jsonb_array_union(t.target_types,s.target_types),
				preferred_regions=jsonb_array_union(t.preferred_regions,s.preferred_regions),
				preferred_stages=jsonb_array_union(t.preferred_stages,s.preferred_stages),
				investment_amount_min=COALESCE(t.investment_amount_min,s.investment_amount_min),
				investment_amount_max=COALESCE(t.investment_amount_max,s.investment_amount_max),
				target_revenue_min=COALESCE(t.target_revenue_min,s.target_revenue_min),
				target_revenue_max=COALESCE(t.target_revenue_max,s.target_revenue_max),
				target_net_profit_min=COALESCE(t.target_net_profit_min,s.target_net_profit_min),
				target_net_profit_max=COALESCE(t.target_net_profit_max,s.target_net_profit_max),
				target_valuation_min=COALESCE(t.target_valuation_min,s.target_valuation_min),
				target_valuation_max=COALESCE(t.target_valuation_max,s.target_valuation_max),
				pe_min=COALESCE(t.pe_min,s.pe_min),pe_max=COALESCE(t.pe_max,s.pe_max),
				listed_status_requirement=COALESCE(t.listed_status_requirement,s.listed_status_requirement),
				control_ratio_min=COALESCE(t.control_ratio_min,s.control_ratio_min),
				profitability_required=COALESCE(t.profitability_required,s.profitability_required),
				extra_constraints=s.extra_constraints||t.extra_constraints,updated_at=NOW()
			FROM buy_demands s WHERE t.id=$1 AND s.id=$2 AND t.status='active' AND s.status='active'`
	} else {
		query = `
			UPDATE sell_projects t SET
				company_name=COALESCE(t.company_name,s.company_name),summary=COALESCE(t.summary,s.summary),
				industries=jsonb_array_union(t.industries,s.industries),
				transaction_types=jsonb_array_union(t.transaction_types,s.transaction_types),
				project_types=jsonb_array_union(t.project_types,s.project_types),
				financing_round=COALESCE(t.financing_round,s.financing_round),
				financing_amount_min=COALESCE(t.financing_amount_min,s.financing_amount_min),
				financing_amount_max=COALESCE(t.financing_amount_max,s.financing_amount_max),
				revenue_min=COALESCE(t.revenue_min,s.revenue_min),revenue_max=COALESCE(t.revenue_max,s.revenue_max),
				net_profit_min=COALESCE(t.net_profit_min,s.net_profit_min),net_profit_max=COALESCE(t.net_profit_max,s.net_profit_max),
				valuation_min=COALESCE(t.valuation_min,s.valuation_min),valuation_max=COALESCE(t.valuation_max,s.valuation_max),
				pe_min=COALESCE(t.pe_min,s.pe_min),pe_max=COALESCE(t.pe_max,s.pe_max),
				province=COALESCE(t.province,s.province),city=COALESCE(t.city,s.city),
				listed_status=COALESCE(t.listed_status,s.listed_status),
				transfer_ratio_min=COALESCE(t.transfer_ratio_min,s.transfer_ratio_min),
				transfer_ratio_max=COALESCE(t.transfer_ratio_max,s.transfer_ratio_max),
				extra_facts=s.extra_facts||t.extra_facts,updated_at=NOW()
			FROM sell_projects s WHERE t.id=$1 AND s.id=$2 AND t.status='active' AND s.status='active'`
	}
	tag, err := tx.Exec(ctx, query, targetID, sourceID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) RevertMerge(ctx context.Context, eventID, operatorID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var entityType string
	var targetID, sourceID int64
	var reasonJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT entity_type,entity_id,source_entity_id,reason_json
		FROM entity_merge_events
		WHERE id=$1 AND action='merge' AND reverted_at IS NULL FOR UPDATE`, eventID).
		Scan(&entityType, &targetID, &sourceID, &reasonJSON)
	if err != nil {
		return err
	}
	var reason struct {
		CandidateID   int64           `json:"candidate_id"`
		SourceItemIDs []int64         `json:"source_item_ids"`
		TargetBefore  json.RawMessage `json:"target_before"`
	}
	if err := json.Unmarshal(reasonJSON, &reason); err != nil {
		return err
	}
	if len(reason.SourceItemIDs) == 0 || len(reason.TargetBefore) == 0 {
		return fmt.Errorf("merge event does not contain a reversible snapshot")
	}
	if err := restoreEntitySnapshot(ctx, tx, entityType, targetID, reason.TargetBefore); err != nil {
		return err
	}
	table, _ := entityTable(entityType)
	if _, err = tx.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET status='active',merged_into_id=NULL,updated_at=NOW() WHERE id=$1`, table),
		sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`
		UPDATE entity_sources SET entity_id=$3,
			relation_type=CASE WHEN item_id=(SELECT first_item_id FROM %s WHERE id=$3)
				THEN 'initial' ELSE 'supplement' END
		WHERE entity_type=$1 AND entity_id=$2 AND item_id=ANY($4)`, table),
		entityType, targetID, sourceID, reason.SourceItemIDs); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE ingestion_items SET entity_id=$3,was_duplicate=false
		WHERE detected_type=$1 AND entity_id=$2 AND id=ANY($4)`,
		entityType, targetID, sourceID, reason.SourceItemIDs); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM entity_embeddings WHERE entity_type=$1 AND entity_id IN ($2,$3)`,
		entityType, targetID, sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM embedding_jobs WHERE entity_type=$1 AND entity_id IN ($2,$3)`,
		entityType, targetID, sourceID); err != nil {
		return err
	}
	if err = queueEmbedding(ctx, tx, entityType, targetID); err != nil {
		return err
	}
	if err = queueEmbedding(ctx, tx, entityType, sourceID); err != nil {
		return err
	}
	if err = recordEntityVersion(ctx, tx, entityType, targetID, reason.SourceItemIDs[0], operatorID, []string{"reverted_merge"}); err != nil {
		return err
	}
	if err = recordEntityVersion(ctx, tx, entityType, sourceID, reason.SourceItemIDs[0], operatorID, []string{"reactivated"}); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE entity_merge_events SET reverted_at=NOW() WHERE id=$1`, eventID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE duplicate_candidates SET status='reverted',resolved_by=$2,resolved_at=NOW()
		WHERE id=$1`, reason.CandidateID, operatorID); err != nil {
		return err
	}
	revertReason, _ := json.Marshal(map[string]any{"reverted_event_id": eventID})
	if _, err = tx.Exec(ctx, `
		INSERT INTO entity_merge_events(
			entity_type,entity_id,source_entity_id,source_item_id,action,merge_method,reason_json,operator_id)
		VALUES($1,$2,$3,$4,'split','revert',$5,$6)`,
		entityType, targetID, sourceID, reason.SourceItemIDs[0], revertReason, operatorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func restoreEntitySnapshot(ctx context.Context, tx pgx.Tx, entityType string, entityID int64, snapshot json.RawMessage) error {
	var query string
	if entityType == "buy_demand" {
		query = `
			UPDATE buy_demands t SET
				title=r.title,buyer_name=r.buyer_name,buyer_type=r.buyer_type,summary=r.summary,
				industries=r.industries,transaction_types=r.transaction_types,target_types=r.target_types,
				preferred_regions=r.preferred_regions,preferred_stages=r.preferred_stages,
				investment_amount_min=r.investment_amount_min,investment_amount_max=r.investment_amount_max,
				target_revenue_min=r.target_revenue_min,target_revenue_max=r.target_revenue_max,
				target_net_profit_min=r.target_net_profit_min,target_net_profit_max=r.target_net_profit_max,
				target_valuation_min=r.target_valuation_min,target_valuation_max=r.target_valuation_max,
				pe_min=r.pe_min,pe_max=r.pe_max,listed_status_requirement=r.listed_status_requirement,
				control_ratio_min=r.control_ratio_min,profitability_required=r.profitability_required,
				extra_constraints=r.extra_constraints,updated_at=NOW()
			FROM jsonb_populate_record(NULL::buy_demands,$2::jsonb) r WHERE t.id=$1`
	} else {
		query = `
			UPDATE sell_projects t SET
				title=r.title,company_name=r.company_name,summary=r.summary,
				industries=r.industries,transaction_types=r.transaction_types,project_types=r.project_types,
				financing_round=r.financing_round,financing_amount_min=r.financing_amount_min,
				financing_amount_max=r.financing_amount_max,revenue_min=r.revenue_min,revenue_max=r.revenue_max,
				net_profit_min=r.net_profit_min,net_profit_max=r.net_profit_max,
				valuation_min=r.valuation_min,valuation_max=r.valuation_max,pe_min=r.pe_min,pe_max=r.pe_max,
				province=r.province,city=r.city,listed_status=r.listed_status,
				transfer_ratio_min=r.transfer_ratio_min,transfer_ratio_max=r.transfer_ratio_max,
				extra_facts=r.extra_facts,updated_at=NOW()
			FROM jsonb_populate_record(NULL::sell_projects,$2::jsonb) r WHERE t.id=$1`
	}
	_, err := tx.Exec(ctx, query, entityID, snapshot)
	return err
}

func recordEntityVersion(ctx context.Context, tx pgx.Tx, entityType string, entityID, sourceItemID, userID int64, fields []string) error {
	table, _ := entityTable(entityType)
	var snapshot []byte
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT to_jsonb(t) FROM %s t WHERE id=$1`, table), entityID).Scan(&snapshot); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_number),0)+1 FROM entity_versions
		WHERE entity_type=$1 AND entity_id=$2`, entityType, entityID).Scan(&version); err != nil {
		return err
	}
	changed, _ := json.Marshal(fields)
	_, err := tx.Exec(ctx, `
		INSERT INTO entity_versions(
			entity_type,entity_id,version_number,snapshot_json,changed_fields,source_item_id,created_by)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0))`,
		entityType, entityID, version, snapshot, changed, sourceItemID, userID)
	return err
}
