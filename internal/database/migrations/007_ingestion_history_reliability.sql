ALTER TABLE ingestion_batches
    ADD COLUMN IF NOT EXISTS client_request_id VARCHAR(100);

CREATE UNIQUE INDEX IF NOT EXISTS idx_batches_user_client_request
    ON ingestion_batches(created_by, client_request_id);

CREATE INDEX IF NOT EXISTS idx_batches_user_created
    ON ingestion_batches(created_by, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_batches_processing_started
    ON ingestion_batches(started_at)
    WHERE status = 'processing';
