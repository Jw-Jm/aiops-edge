-- Canonical report read model scope. Legacy rows without an authoritative
-- tenant/cluster remain unreadable by the production browser workspace.
ALTER TABLE reports ADD COLUMN tenant_id CHAR(36) NULL;
-- statement-breakpoint
ALTER TABLE reports ADD COLUMN cluster_id CHAR(36) NULL;
-- statement-breakpoint
CREATE INDEX idx_reports_tenant_cluster_created ON reports (tenant_id, cluster_id, created_at);
