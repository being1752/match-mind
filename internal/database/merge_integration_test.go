package database

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func TestMergeEntitiesIntegration(t *testing.T) {
	if os.Getenv("RUN_DB_INTEGRATION") != "1" {
		t.Skip("set RUN_DB_INTEGRATION=1 to run")
	}
	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	pool, err := Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewStore(pool)
	stamp := time.Now().UnixNano()
	username := fmt.Sprintf("merge_test_%d", stamp)
	user, err := store.CreateUser(ctx, username, "Integration-9381", "合并集成测试")
	if err != nil {
		t.Fatal(err)
	}
	var batchIDs, itemIDs, entityIDs []int64
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM entity_merge_events WHERE entity_type='buy_demand' AND (entity_id=ANY($1) OR source_entity_id=ANY($1))`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM entity_versions WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM duplicate_candidates WHERE source_entity_id=ANY($1) OR candidate_entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM embedding_jobs WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM entity_embeddings WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM entity_sources WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM buy_demands WHERE id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM ingestion_items WHERE id=ANY($1)`, itemIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM ingestion_batches WHERE id=ANY($1)`, batchIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user.ID)
	}()

	createEntity := func(title, raw, hash, industry string) int64 {
		t.Helper()
		batchID, _, err := store.CreateBatch(ctx, raw, "merge-integration", "", "", user.ID)
		if err != nil {
			t.Fatal(err)
		}
		batchIDs = append(batchIDs, batchID)
		var itemID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO ingestion_items(batch_id,item_index,raw_text,detected_type,extraction_json)
			VALUES($1,1,$2,'buy_demand','{}') RETURNING id`, batchID, raw).Scan(&itemID)
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, itemID)
		var entityID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO buy_demands(title,industries,canonical_hash,first_item_id,owner_user_id)
			VALUES($1,jsonb_build_array($2::text),$3,$4,$5) RETURNING id`,
			title, industry, hash, itemID, user.ID).Scan(&entityID)
		if err != nil {
			t.Fatal(err)
		}
		entityIDs = append(entityIDs, entityID)
		_, err = pool.Exec(ctx, `
			INSERT INTO entity_sources(entity_type,entity_id,item_id)
			VALUES('buy_demand',$1,$2)`, entityID, itemID)
		if err != nil {
			t.Fatal(err)
		}
		return entityID
	}

	targetID := createEntity("集成测试目标", "原文一", fmt.Sprintf("%064x", stamp), "医疗")
	sourceID := createEntity("集成测试来源", "原文二", fmt.Sprintf("%064x", stamp+1), "干细胞")
	var candidateID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO duplicate_candidates(
			source_entity_type,source_entity_id,candidate_entity_type,candidate_entity_id,
			source_user_id,candidate_user_id,vector_score,final_score)
		VALUES('buy_demand',$1,'buy_demand',$2,$3,$3,0.9,0.9) RETURNING id`,
		sourceID, targetID, user.ID).Scan(&candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MergeEntities(ctx, candidateID, user.ID); err != nil {
		t.Fatal(err)
	}
	var sourceStatus string
	var industries, sourceCount int
	err = pool.QueryRow(ctx, `
		SELECT (SELECT status FROM buy_demands WHERE id=$1),
			(SELECT jsonb_array_length(industries) FROM buy_demands WHERE id=$2),
			(SELECT COUNT(*) FROM entity_sources WHERE entity_type='buy_demand' AND entity_id=$2)`,
		sourceID, targetID).Scan(&sourceStatus, &industries, &sourceCount)
	if err != nil {
		t.Fatal(err)
	}
	if sourceStatus != "merged" || industries != 2 || sourceCount != 2 {
		t.Fatalf("unexpected merge result: status=%s industries=%d sources=%d", sourceStatus, industries, sourceCount)
	}
	var eventID int64
	err = pool.QueryRow(ctx, `
		SELECT id FROM entity_merge_events
		WHERE entity_type='buy_demand' AND entity_id=$1 AND source_entity_id=$2 AND action='merge'
		ORDER BY id DESC LIMIT 1`, targetID, sourceID).Scan(&eventID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevertMerge(ctx, eventID, user.ID); err != nil {
		t.Fatal(err)
	}
	var targetIndustries, targetSources, sourceSources int
	err = pool.QueryRow(ctx, `
		SELECT (SELECT jsonb_array_length(industries) FROM buy_demands WHERE id=$1),
			(SELECT COUNT(*) FROM entity_sources WHERE entity_type='buy_demand' AND entity_id=$1),
			(SELECT COUNT(*) FROM entity_sources WHERE entity_type='buy_demand' AND entity_id=$2)`,
		targetID, sourceID).Scan(&targetIndustries, &targetSources, &sourceSources)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `SELECT status FROM buy_demands WHERE id=$1`, sourceID).Scan(&sourceStatus)
	if err != nil {
		t.Fatal(err)
	}
	if sourceStatus != "active" || targetIndustries != 1 || targetSources != 1 || sourceSources != 1 {
		t.Fatalf("unexpected revert result: status=%s industries=%d target_sources=%d source_sources=%d",
			sourceStatus, targetIndustries, targetSources, sourceSources)
	}
}

func TestVectorMatchingIntegration(t *testing.T) {
	if os.Getenv("RUN_DB_INTEGRATION") != "1" {
		t.Skip("set RUN_DB_INTEGRATION=1 to run")
	}
	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	pool, err := Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewStore(pool)
	stamp := time.Now().UnixNano()
	buyUser, err := store.CreateUser(ctx, fmt.Sprintf("match_buy_%d", stamp), "Integration-9381", "跨账号买方")
	if err != nil {
		t.Fatal(err)
	}
	sellUser, err := store.CreateUser(ctx, fmt.Sprintf("match_sell_%d", stamp), "Integration-9381", "跨账号卖方")
	if err != nil {
		t.Fatal(err)
	}
	hash1 := fmt.Sprintf("%064x", stamp)
	hash2 := fmt.Sprintf("%064x", stamp+1)
	var buyID, sellID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO buy_demands(title,industries,transaction_types,canonical_hash,owner_user_id)
		VALUES('向量集成测试买方','["医疗"]','["acquisition"]',$1,$2) RETURNING id`, hash1, buyUser.ID).Scan(&buyID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM match_results WHERE buy_demand_id=$1 OR sell_project_id=$2`, buyID, sellID)
		_, _ = pool.Exec(ctx, `DELETE FROM entity_embeddings WHERE (entity_type='buy_demand' AND entity_id=$1) OR (entity_type='sell_project' AND entity_id=$2)`, buyID, sellID)
		_, _ = pool.Exec(ctx, `DELETE FROM embedding_jobs WHERE (entity_type='buy_demand' AND entity_id=$1) OR (entity_type='sell_project' AND entity_id=$2)`, buyID, sellID)
		_, _ = pool.Exec(ctx, `DELETE FROM buy_demands WHERE id=$1`, buyID)
		_, _ = pool.Exec(ctx, `DELETE FROM sell_projects WHERE id=$1`, sellID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, buyUser.ID, sellUser.ID)
	}()
	err = pool.QueryRow(ctx, `
		INSERT INTO sell_projects(title,industries,transaction_types,canonical_hash,owner_user_id)
		VALUES('向量集成测试卖方','["医疗"]','["acquisition"]',$1,$2) RETURNING id`, hash2, sellUser.ID).Scan(&sellID)
	if err != nil {
		t.Fatal(err)
	}
	var buyJobID, sellJobID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO embedding_jobs(entity_type,entity_id)
		VALUES('buy_demand',$1) RETURNING id`, buyID).Scan(&buyJobID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `
		INSERT INTO embedding_jobs(entity_type,entity_id)
		VALUES('sell_project',$1) RETURNING id`, sellID).Scan(&sellJobID)
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, 1024)
	for index := range vector {
		vector[index] = 0.03125
	}
	buyJob := EmbeddingJob{ID: buyJobID, EntityType: "buy_demand", EntityID: buyID, EmbeddingType: "matching_v1"}
	sellJob := EmbeddingJob{ID: sellJobID, EntityType: "sell_project", EntityID: sellID, EmbeddingType: "matching_v1"}
	if err := store.SaveEmbedding(ctx, buyJob, "买方医疗并购", "integration-model", vector); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEmbedding(ctx, sellJob, "卖方医疗并购", "integration-model", vector); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildMatches(ctx, "buy_demand", buyID, "integration-model"); err != nil {
		t.Fatal(err)
	}
	var vectorScore, finalScore float64
	err = pool.QueryRow(ctx, `
		SELECT vector_score,final_score FROM match_results
		WHERE buy_demand_id=$1 AND sell_project_id=$2`, buyID, sellID).
		Scan(&vectorScore, &finalScore)
	if err != nil {
		t.Fatal(err)
	}
	if vectorScore < 0.999 || finalScore < 0.9 {
		t.Fatalf("unexpected scores: vector=%f final=%f", vectorScore, finalScore)
	}
}
