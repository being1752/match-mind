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

ALTER TABLE buy_demands DROP CONSTRAINT IF EXISTS buy_demands_first_item_id_fkey;
ALTER TABLE buy_demands ADD CONSTRAINT buy_demands_first_item_id_fkey
    FOREIGN KEY(first_item_id) REFERENCES ingestion_items(id) ON DELETE SET NULL;
ALTER TABLE sell_projects DROP CONSTRAINT IF EXISTS sell_projects_first_item_id_fkey;
ALTER TABLE sell_projects ADD CONSTRAINT sell_projects_first_item_id_fkey
    FOREIGN KEY(first_item_id) REFERENCES ingestion_items(id) ON DELETE SET NULL;
ALTER TABLE entity_versions DROP CONSTRAINT IF EXISTS entity_versions_source_item_id_fkey;
ALTER TABLE entity_versions ADD CONSTRAINT entity_versions_source_item_id_fkey
    FOREIGN KEY(source_item_id) REFERENCES ingestion_items(id) ON DELETE SET NULL;
ALTER TABLE entity_merge_events DROP CONSTRAINT IF EXISTS entity_merge_events_source_item_id_fkey;
ALTER TABLE entity_merge_events ADD CONSTRAINT entity_merge_events_source_item_id_fkey
    FOREIGN KEY(source_item_id) REFERENCES ingestion_items(id) ON DELETE SET NULL;

INSERT INTO entity_versions(entity_type,entity_id,version_number,snapshot_json,source_item_id,created_by)
SELECT 'buy_demand',b.id,1,to_jsonb(b),b.first_item_id,ib.created_by
FROM buy_demands b
LEFT JOIN ingestion_items i ON i.id=b.first_item_id
LEFT JOIN ingestion_batches ib ON ib.id=i.batch_id
ON CONFLICT(entity_type,entity_id,version_number) DO NOTHING;

INSERT INTO entity_versions(entity_type,entity_id,version_number,snapshot_json,source_item_id,created_by)
SELECT 'sell_project',s.id,1,to_jsonb(s),s.first_item_id,ib.created_by
FROM sell_projects s
LEFT JOIN ingestion_items i ON i.id=s.first_item_id
LEFT JOIN ingestion_batches ib ON ib.id=i.batch_id
ON CONFLICT(entity_type,entity_id,version_number) DO NOTHING;

INSERT INTO entity_merge_events(
    entity_type,entity_id,source_item_id,action,merge_method,reason_json,operator_id)
SELECT es.entity_type,es.entity_id,es.item_id,'exact_duplicate','canonical_hash',
    jsonb_build_object('relation_type','duplicate_source'),b.created_by
FROM entity_sources es
JOIN ingestion_items i ON i.id=es.item_id
JOIN ingestion_batches b ON b.id=i.batch_id
WHERE es.relation_type='duplicate_source'
AND NOT EXISTS (
    SELECT 1 FROM entity_merge_events e
    WHERE e.entity_type=es.entity_type AND e.entity_id=es.entity_id
      AND e.source_item_id=es.item_id AND e.action='exact_duplicate'
);
