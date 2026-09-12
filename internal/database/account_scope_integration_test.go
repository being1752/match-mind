package database

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"matchmind/internal/models"
)

func TestExactDuplicateIsScopedToAccountIntegration(t *testing.T) {
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
	userA, err := store.CreateUser(ctx, fmt.Sprintf("scope_a_%d", stamp), "Integration-9381", "账号A")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := store.CreateUser(ctx, fmt.Sprintf("scope_b_%d", stamp), "Integration-9381", "账号B")
	if err != nil {
		t.Fatal(err)
	}
	var batchIDs []int64
	var entityIDs []int64
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM entity_merge_events WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM entity_versions WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM embedding_jobs WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM entity_sources WHERE entity_type='buy_demand' AND entity_id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM buy_demands WHERE id=ANY($1)`, entityIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM ingestion_items WHERE batch_id=ANY($1)`, batchIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM ingestion_batches WHERE id=ANY($1)`, batchIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, userA.ID, userB.ID)
	}()

	raw := fmt.Sprintf("账号隔离完全重复测试-%d", stamp)
	structured, _ := json.Marshal(map[string]any{"title": raw, "industries": []string{"测试行业"}})
	response := models.StructuredResponse{Items: []models.StructuredItem{{
		ItemIndex: 1, RawText: raw, DetectedType: "buy_demand",
		ClassificationConfidence: 1, Title: raw, StructuredData: structured,
	}}}
	save := func(userID int64) (int64, bool) {
		t.Helper()
		batchID, _, err := store.CreateBatch(ctx, raw, "account-scope-integration", "", "", userID)
		if err != nil {
			t.Fatal(err)
		}
		batchIDs = append(batchIDs, batchID)
		if err := store.SaveStructuredBatch(ctx, batchID, response, "integration-model"); err != nil {
			t.Fatal(err)
		}
		var entityID int64
		var duplicate bool
		if err := pool.QueryRow(ctx, `SELECT entity_id,was_duplicate FROM ingestion_items WHERE batch_id=$1`, batchID).Scan(&entityID, &duplicate); err != nil {
			t.Fatal(err)
		}
		entityIDs = append(entityIDs, entityID)
		return entityID, duplicate
	}

	entityA, duplicateA := save(userA.ID)
	entityB, duplicateB := save(userB.ID)
	entityAAgain, duplicateAAgain := save(userA.ID)
	if duplicateA || duplicateB {
		t.Fatal("first ingestion for each account must create an independent entity")
	}
	if entityA == entityB {
		t.Fatal("different accounts must keep identical information as independent entities")
	}
	if entityAAgain != entityA || !duplicateAAgain {
		t.Fatal("same account must associate an exact duplicate with its existing entity")
	}
}
