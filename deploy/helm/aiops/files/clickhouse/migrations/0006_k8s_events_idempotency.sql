-- Stable event identity makes collector retries and ingest-WAL replay
-- idempotent. Existing rows retain an empty ID and are not silently assigned a
-- guessed identity; the cutover runbook must backfill or quarantine them before
-- relying on deduplication for historical data.
ALTER TABLE observability.k8s_events ADD COLUMN IF NOT EXISTS event_id String DEFAULT '';
-- statement-breakpoint
ALTER TABLE observability.k8s_events MODIFY ORDER BY (tenant_id, cluster_id, event_id);
