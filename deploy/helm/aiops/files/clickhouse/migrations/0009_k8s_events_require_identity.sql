-- 0009-k8s-events-require-identity
-- ClickHouse MODIFY COLUMN ... String preserves a prior DEFAULT expression.
-- Remove it explicitly so every accepted event must carry a canonical identity;
-- legacy writers can no longer synthesize an empty event_id.
ALTER TABLE observability.k8s_events
    MODIFY COLUMN event_id String REMOVE DEFAULT;
