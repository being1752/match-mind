CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS ingestion_batches (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    raw_text TEXT NOT NULL,
    source_name VARCHAR(100) NOT NULL DEFAULT '人工录入',
    source_file VARCHAR(255),
    status VARCHAR(30) NOT NULL DEFAULT 'pending',
    total_items INTEGER NOT NULL DEFAULT 0,
    buy_count INTEGER NOT NULL DEFAULT 0,
    sell_count INTEGER NOT NULL DEFAULT 0,
    unknown_count INTEGER NOT NULL DEFAULT 0,
    duplicate_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS ingestion_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id BIGINT NOT NULL REFERENCES ingestion_batches(id) ON DELETE CASCADE,
    item_index INTEGER NOT NULL,
    raw_text TEXT NOT NULL,
    detected_type VARCHAR(30) NOT NULL,
    classification_confidence NUMERIC(5,4),
    processing_status VARCHAR(30) NOT NULL DEFAULT 'completed',
    extraction_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    entity_id BIGINT,
    was_duplicate BOOLEAN NOT NULL DEFAULT FALSE,
    model_name VARCHAR(100),
    schema_version VARCHAR(30) NOT NULL DEFAULT 'v1',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(batch_id, item_index)
);

CREATE TABLE IF NOT EXISTS buy_demands (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title VARCHAR(500) NOT NULL,
    buyer_name VARCHAR(255),
    buyer_type VARCHAR(100),
    summary TEXT,
    industries JSONB NOT NULL DEFAULT '[]'::jsonb,
    transaction_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    target_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    preferred_regions JSONB NOT NULL DEFAULT '[]'::jsonb,
    preferred_stages JSONB NOT NULL DEFAULT '[]'::jsonb,
    investment_amount_min NUMERIC(20,2),
    investment_amount_max NUMERIC(20,2),
    target_revenue_min NUMERIC(20,2),
    target_revenue_max NUMERIC(20,2),
    target_net_profit_min NUMERIC(20,2),
    target_net_profit_max NUMERIC(20,2),
    target_valuation_min NUMERIC(20,2),
    target_valuation_max NUMERIC(20,2),
    pe_min NUMERIC(10,2),
    pe_max NUMERIC(10,2),
    listed_status_requirement VARCHAR(100),
    control_ratio_min NUMERIC(8,4),
    profitability_required BOOLEAN,
    extra_constraints JSONB NOT NULL DEFAULT '{}'::jsonb,
    canonical_hash CHAR(64) NOT NULL UNIQUE,
    status VARCHAR(30) NOT NULL DEFAULT 'active',
    first_item_id BIGINT REFERENCES ingestion_items(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sell_projects (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title VARCHAR(500) NOT NULL,
    company_name VARCHAR(255),
    summary TEXT,
    industries JSONB NOT NULL DEFAULT '[]'::jsonb,
    transaction_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    project_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    financing_round VARCHAR(100),
    financing_amount_min NUMERIC(20,2),
    financing_amount_max NUMERIC(20,2),
    revenue_min NUMERIC(20,2),
    revenue_max NUMERIC(20,2),
    net_profit_min NUMERIC(20,2),
    net_profit_max NUMERIC(20,2),
    valuation_min NUMERIC(20,2),
    valuation_max NUMERIC(20,2),
    pe_min NUMERIC(10,2),
    pe_max NUMERIC(10,2),
    province VARCHAR(100),
    city VARCHAR(100),
    listed_status VARCHAR(100),
    transfer_ratio_min NUMERIC(8,4),
    transfer_ratio_max NUMERIC(8,4),
    extra_facts JSONB NOT NULL DEFAULT '{}'::jsonb,
    canonical_hash CHAR(64) NOT NULL UNIQUE,
    status VARCHAR(30) NOT NULL DEFAULT 'active',
    first_item_id BIGINT REFERENCES ingestion_items(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS entity_sources (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type VARCHAR(30) NOT NULL,
    entity_id BIGINT NOT NULL,
    item_id BIGINT NOT NULL REFERENCES ingestion_items(id) ON DELETE CASCADE,
    relation_type VARCHAR(30) NOT NULL DEFAULT 'initial',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(entity_type, entity_id, item_id)
);

CREATE TABLE IF NOT EXISTS entity_embeddings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type VARCHAR(30) NOT NULL,
    entity_id BIGINT NOT NULL,
    embedding_type VARCHAR(30) NOT NULL,
    model_name VARCHAR(100) NOT NULL,
    content_text TEXT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    embedding VECTOR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(entity_type, entity_id, embedding_type, model_name)
);

CREATE INDEX IF NOT EXISTS idx_batches_status_created ON ingestion_batches(status, created_at);
CREATE INDEX IF NOT EXISTS idx_items_batch ON ingestion_items(batch_id, item_index);
CREATE INDEX IF NOT EXISTS idx_buy_status ON buy_demands(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sell_status ON sell_projects(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_buy_industries ON buy_demands USING GIN(industries);
CREATE INDEX IF NOT EXISTS idx_sell_industries ON sell_projects USING GIN(industries);

