CREATE TABLE IF NOT EXISTS users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username VARCHAR(80) NOT NULL,
    password_hash TEXT NOT NULL,
    display_name VARCHAR(100) NOT NULL DEFAULT '',
    wechat_openid VARCHAR(128),
    status VARCHAR(30) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower ON users(LOWER(username));
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_wechat_openid ON users(wechat_openid) WHERE wechat_openid IS NOT NULL;

CREATE OR REPLACE FUNCTION jsonb_array_union(left_value JSONB, right_value JSONB)
RETURNS JSONB
LANGUAGE SQL
IMMUTABLE
AS $$
    SELECT COALESCE(jsonb_agg(value ORDER BY value::text), '[]'::jsonb)
    FROM (
        SELECT DISTINCT value
        FROM jsonb_array_elements(
            COALESCE(left_value, '[]'::jsonb) || COALESCE(right_value, '[]'::jsonb)
        )
    ) values_union
$$;

ALTER TABLE ingestion_batches ADD COLUMN IF NOT EXISTS created_by BIGINT REFERENCES users(id);
CREATE INDEX IF NOT EXISTS idx_batches_created_by ON ingestion_batches(created_by, created_at DESC);

ALTER TABLE buy_demands ADD COLUMN IF NOT EXISTS merged_into_id BIGINT REFERENCES buy_demands(id);
ALTER TABLE sell_projects ADD COLUMN IF NOT EXISTS merged_into_id BIGINT REFERENCES sell_projects(id);

-- The first release standardizes on 1024 dimensions. A model change requires a
-- new migration and a full rebuild; vectors from different models are not mixed.
ALTER TABLE entity_embeddings
    ALTER COLUMN embedding TYPE VECTOR(1024)
    USING embedding::VECTOR(1024);

ALTER TABLE entity_embeddings ADD COLUMN IF NOT EXISTS status VARCHAR(30) NOT NULL DEFAULT 'completed';
ALTER TABLE entity_embeddings ADD COLUMN IF NOT EXISTS error_message TEXT;

CREATE INDEX IF NOT EXISTS idx_embeddings_entity
    ON entity_embeddings(entity_type, entity_id, embedding_type, model_name);
CREATE INDEX IF NOT EXISTS idx_embeddings_hnsw
    ON entity_embeddings USING hnsw (embedding vector_cosine_ops)
    WHERE status = 'completed' AND embedding IS NOT NULL;

CREATE TABLE IF NOT EXISTS embedding_jobs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type VARCHAR(30) NOT NULL,
    entity_id BIGINT NOT NULL,
    embedding_type VARCHAR(30) NOT NULL DEFAULT 'matching_v1',
    status VARCHAR(30) NOT NULL DEFAULT 'pending',
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 5,
    error_message TEXT,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(entity_type, entity_id, embedding_type)
);

CREATE INDEX IF NOT EXISTS idx_embedding_jobs_poll
    ON embedding_jobs(status, available_at, created_at);

CREATE TABLE IF NOT EXISTS duplicate_candidates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_entity_type VARCHAR(30) NOT NULL,
    source_entity_id BIGINT NOT NULL,
    candidate_entity_type VARCHAR(30) NOT NULL,
    candidate_entity_id BIGINT NOT NULL,
    source_user_id BIGINT REFERENCES users(id),
    candidate_user_id BIGINT REFERENCES users(id),
    vector_score NUMERIC(8,6),
    structured_score NUMERIC(8,6),
    final_score NUMERIC(8,6) NOT NULL,
    reason_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(30) NOT NULL DEFAULT 'pending',
    resolved_by BIGINT REFERENCES users(id),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_entity_type, source_entity_id, candidate_entity_type, candidate_entity_id)
);

CREATE INDEX IF NOT EXISTS idx_duplicate_candidates_status
    ON duplicate_candidates(status, created_at DESC);

CREATE TABLE IF NOT EXISTS entity_merge_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type VARCHAR(30) NOT NULL,
    entity_id BIGINT NOT NULL,
    source_entity_id BIGINT,
    source_item_id BIGINT REFERENCES ingestion_items(id),
    action VARCHAR(30) NOT NULL,
    merge_method VARCHAR(30) NOT NULL,
    similarity_score NUMERIC(8,6),
    reason_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    operator_id BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reverted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_merge_events_entity
    ON entity_merge_events(entity_type, entity_id, created_at DESC);

CREATE TABLE IF NOT EXISTS entity_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type VARCHAR(30) NOT NULL,
    entity_id BIGINT NOT NULL,
    version_number INTEGER NOT NULL,
    snapshot_json JSONB NOT NULL,
    changed_fields JSONB NOT NULL DEFAULT '[]'::jsonb,
    source_item_id BIGINT REFERENCES ingestion_items(id),
    created_by BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(entity_type, entity_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_entity_versions_entity
    ON entity_versions(entity_type, entity_id, version_number DESC);

CREATE TABLE IF NOT EXISTS match_results (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    buy_demand_id BIGINT NOT NULL REFERENCES buy_demands(id),
    sell_project_id BIGINT NOT NULL REFERENCES sell_projects(id),
    structured_score NUMERIC(8,6) NOT NULL DEFAULT 0,
    vector_score NUMERIC(8,6) NOT NULL DEFAULT 0,
    final_score NUMERIC(8,6) NOT NULL DEFAULT 0,
    reasons JSONB NOT NULL DEFAULT '[]'::jsonb,
    scoring_version VARCHAR(30) NOT NULL DEFAULT 'v1',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(buy_demand_id, sell_project_id, scoring_version)
);

CREATE INDEX IF NOT EXISTS idx_match_results_buy ON match_results(buy_demand_id, final_score DESC);
CREATE INDEX IF NOT EXISTS idx_match_results_sell ON match_results(sell_project_id, final_score DESC);
