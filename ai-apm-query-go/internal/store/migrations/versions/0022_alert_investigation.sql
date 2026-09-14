-- 0022: alert → investigation governed linking.
--
-- The alert event itself stays authoritative in ClickHouse (alert_events); these
-- two tables only record the *decision* to open an investigation and the single
-- Run it produced. No new Incident state machine is introduced.
--
-- Policies are per real cluster. An absent row is strictly equivalent to
-- `manual`, so the migration seeds nothing.

CREATE TABLE IF NOT EXISTS ai_alert_investigation_policies (
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  mode VARCHAR(24) NOT NULL DEFAULT 'manual',
  minimum_severity VARCHAR(16) NOT NULL DEFAULT 'critical',
  max_concurrent INT NOT NULL DEFAULT 2,
  max_per_hour INT NOT NULL DEFAULT 10,
  updated_by VARCHAR(255) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, cluster_id),
  CONSTRAINT chk_alert_investigation_mode CHECK (mode IN ('manual','draft','auto_readonly'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- statement-breakpoint

CREATE TABLE IF NOT EXISTS ai_alert_run_links (
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  alert_event_id VARCHAR(128) NOT NULL,
  alert_signature VARCHAR(255) NOT NULL,
  run_id CHAR(36) NULL,
  decision VARCHAR(24) NOT NULL,
  reason_code VARCHAR(64) NULL,
  source_resolved TINYINT NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, cluster_id, alert_event_id),
  UNIQUE KEY uk_alert_run (tenant_id, cluster_id, run_id),
  CONSTRAINT chk_alert_run_decision CHECK (decision IN ('skipped','draft','run'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
