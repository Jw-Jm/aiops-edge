-- mysql/0013-session-scope
-- The active tenant/cluster is a server-side session property.  It is never
-- inferred from a browser header or a first list item.
ALTER TABLE auth_sessions
  ADD COLUMN active_tenant_id VARCHAR(64) NULL,
  ADD COLUMN active_cluster_id CHAR(36) NULL,
  ADD COLUMN authorization_version BIGINT NOT NULL DEFAULT 1;
-- statement-breakpoint
CREATE INDEX idx_auth_sessions_scope
  ON auth_sessions(user_uuid, active_tenant_id, active_cluster_id, status);
