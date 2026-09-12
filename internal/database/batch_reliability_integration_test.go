package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func TestBatchHistoryIdempotencyAndOwnershipIntegration(t *testing.T) {
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
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	stamp := time.Now().UnixNano()
	userA, err := store.CreateUser(ctx, fmt.Sprintf("batch_a_%d", stamp), "Integration-9381", "批次账号A")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := store.CreateUser(ctx, fmt.Sprintf("batch_b_%d", stamp), "Integration-9381", "批次账号B")
	if err != nil {
		t.Fatal(err)
	}
	var batchIDs []int64
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ingestion_batches WHERE id=ANY($1)`, batchIDs)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, userA.ID, userB.ID)
	}()

	requestID := fmt.Sprintf("request-%d", stamp)
	batchA, status, err := store.CreateBatch(ctx, "第一份原文", "integration", "", requestID, userA.ID)
	if err != nil || status != "pending" {
		t.Fatalf("create first batch: status=%q err=%v", status, err)
	}
	batchIDs = append(batchIDs, batchA)
	batchARepeat, _, err := store.CreateBatch(ctx, "不应覆盖的重复请求", "integration", "", requestID, userA.ID)
	if err != nil || batchARepeat != batchA {
		t.Fatalf("idempotent create returned id=%d want=%d err=%v", batchARepeat, batchA, err)
	}
	batchB, _, err := store.CreateBatch(ctx, "另一账号原文", "integration", "", requestID, userB.ID)
	if err != nil || batchB == batchA {
		t.Fatalf("different user should get independent batch: id=%d err=%v", batchB, err)
	}
	batchIDs = append(batchIDs, batchB)

	items, total, err := store.ListBatches(ctx, userA.ID, 20, 0)
	if err != nil || total < 1 || len(items) < 1 || items[0].ID != batchA {
		t.Fatalf("list own batches: total=%d items=%v err=%v", total, items, err)
	}
	if _, err := store.GetBatch(ctx, batchA, userB.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-account batch read should be hidden, got %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE ingestion_batches SET status='failed' WHERE id=$1`, batchA); err != nil {
		t.Fatal(err)
	}
	if err := store.RetryBatch(ctx, batchA, userB.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-account retry should be rejected, got %v", err)
	}
	if err := store.RetryBatch(ctx, batchA, userA.ID); err != nil {
		t.Fatalf("owner retry failed: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE ingestion_batches SET status='processing',started_at=NOW()-INTERVAL '11 minutes' WHERE id=$1`, batchA); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverStaleBatches(ctx, 10*time.Minute)
	if err != nil || recovered < 1 {
		t.Fatalf("recover stale batch: count=%d err=%v", recovered, err)
	}
	var finalStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM ingestion_batches WHERE id=$1`, batchA).Scan(&finalStatus); err != nil || finalStatus != "retry" {
		t.Fatalf("recovered status=%q err=%v", finalStatus, err)
	}
}
