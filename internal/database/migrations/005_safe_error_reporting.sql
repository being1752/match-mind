ALTER TABLE ingestion_batches ADD COLUMN IF NOT EXISTS error_code VARCHAR(50);
