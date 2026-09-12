-- Embeddings from different models cannot be compared, even at the same size.
-- Keep business entities and audit history while invalidating derived results.
-- The legacy worker ignores model_switch_pending. The new worker requeues these
-- jobs only after the Tencent AI service and updated Go API start together.
DELETE FROM match_results;

DELETE FROM duplicate_candidates
WHERE status = 'pending';

DELETE FROM entity_embeddings
WHERE model_name <> 'kinfra-text-embedding-0.6b';

UPDATE embedding_jobs
SET status = 'model_switch_pending',
    retry_count = 0,
    error_message = NULL,
    available_at = NOW(),
    started_at = NULL,
    completed_at = NULL,
    updated_at = NOW();
