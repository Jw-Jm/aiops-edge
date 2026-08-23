-- mysql/0003-platform-audit
-- V9.2 Phase 4：platform_audit_events（平台安全审计，独立于 AI audit）。
-- owner: query-api；记录认证、授权、Cluster Access、Secret access 等平台级安全事件。
-- 语句用 "-- statement-breakpoint" 分隔。

CREATE TABLE IF NOT EXISTS platform_audit_events (
  audit_id BIGINT AUTO_INCREMENT PRIMARY KEY,
  request_id CHAR(36) NOT NULL DEFAULT '',
  run_id CHAR(36) NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NULL,
  user_id VARCHAR(128) NOT NULL DEFAULT '',
  service_identity VARCHAR(128) NOT NULL DEFAULT '',
  action VARCHAR(128) NOT NULL DEFAULT '',
  result VARCHAR(32) NOT NULL DEFAULT 'success',
  detail JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  INDEX idx_platform_audit_created (created_at),
  INDEX idx_platform_audit_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
