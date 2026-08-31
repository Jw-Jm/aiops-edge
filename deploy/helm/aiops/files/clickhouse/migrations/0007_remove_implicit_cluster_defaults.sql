-- Remove the historical `cluster_id = 'default'` expressions from the
-- baseline observability tables.  Scope is supplied by the authenticated
-- ingest/query boundary; ClickHouse must never synthesize a tenant/cluster.
-- Existing rows are intentionally not rewritten here.  The release gate must
-- quarantine or explicitly map legacy rows before enabling cross-tenant reads.
ALTER TABLE observability.log_records MODIFY COLUMN cluster_id String;
-- statement-breakpoint
ALTER TABLE observability.service_topology MODIFY COLUMN cluster_id String;
-- statement-breakpoint
ALTER TABLE observability.change_records MODIFY COLUMN cluster_id String;
-- statement-breakpoint
ALTER TABLE observability.trace_spans MODIFY COLUMN cluster_id String;
-- statement-breakpoint
ALTER TABLE observability.alert_events MODIFY COLUMN cluster_id String;
-- statement-breakpoint
ALTER TABLE observability.k8s_events MODIFY COLUMN cluster_id String;
