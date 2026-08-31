-- alert_events tenant isolation contract. Existing rows without a canonical
-- tenant are intentionally not backfilled to a default; the cutover runbook
-- must quarantine/map them before reads switch to this schema.
ALTER TABLE observability.alert_events ADD COLUMN IF NOT EXISTS tenant_id String;
-- statement-breakpoint
ALTER TABLE observability.alert_events MODIFY ORDER BY (tenant_id, cluster_id, timestamp, id);
