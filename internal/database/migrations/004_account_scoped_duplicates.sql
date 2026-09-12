-- Duplicate detection and merging are owner-scoped. Buy/sell matching remains
-- group-wide and intentionally does not filter by owner_user_id.
ALTER TABLE buy_demands ADD COLUMN IF NOT EXISTS owner_user_id BIGINT REFERENCES users(id);
ALTER TABLE sell_projects ADD COLUMN IF NOT EXISTS owner_user_id BIGINT REFERENCES users(id);

UPDATE buy_demands e SET owner_user_id=b.created_by
FROM ingestion_items i JOIN ingestion_batches b ON b.id=i.batch_id
WHERE i.id=e.first_item_id AND e.owner_user_id IS NULL;

UPDATE sell_projects e SET owner_user_id=b.created_by
FROM ingestion_items i JOIN ingestion_batches b ON b.id=i.batch_id
WHERE i.id=e.first_item_id AND e.owner_user_id IS NULL;

ALTER TABLE buy_demands DROP CONSTRAINT IF EXISTS buy_demands_canonical_hash_key;
ALTER TABLE sell_projects DROP CONSTRAINT IF EXISTS sell_projects_canonical_hash_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_buy_owner_canonical_hash
    ON buy_demands(owner_user_id, canonical_hash)
    WHERE owner_user_id IS NOT NULL AND status='active';
CREATE UNIQUE INDEX IF NOT EXISTS idx_sell_owner_canonical_hash
    ON sell_projects(owner_user_id, canonical_hash)
    WHERE owner_user_id IS NOT NULL AND status='active';
CREATE INDEX IF NOT EXISTS idx_buy_owner_status
    ON buy_demands(owner_user_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sell_owner_status
    ON sell_projects(owner_user_id, status, updated_at DESC);

UPDATE duplicate_candidates
SET status='superseded', resolved_at=NOW()
WHERE status='pending'
  AND (source_user_id IS NULL OR candidate_user_id IS NULL OR source_user_id<>candidate_user_id);
