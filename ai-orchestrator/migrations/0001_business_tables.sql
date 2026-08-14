-- 0001_business_tables.sql — P1b 业务状态库 6 张表（MySQL 8.4, utf8mb4）

CREATE TABLE IF NOT EXISTS approval_tasks (
  id           BIGINT PRIMARY KEY AUTO_INCREMENT,
  task_id      VARCHAR(64) NOT NULL UNIQUE,
  service_name VARCHAR(128),
  status       VARCHAR(24),
  plan         TEXT,
  script       TEXT,
  risk_score   FLOAT,
  risk_reason  TEXT,
  diagnosis    TEXT,
  report       TEXT,
  requester    VARCHAR(64),
  created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
  decided_at   DATETIME NULL,
  decision_by  VARCHAR(64) NULL,
  KEY idx_approval_status (status),
  KEY idx_approval_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS audit_logs (
  id             BIGINT PRIMARY KEY AUTO_INCREMENT,
  task_id        VARCHAR(64),
  action         VARCHAR(64),
  operator       VARCHAR(64),
  target_service VARCHAR(128),
  command        TEXT,
  result         VARCHAR(24),
  detail         JSON,
  created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_audit_action_created (action, created_at),
  KEY idx_audit_operator_created (operator, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agents (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  name       VARCHAR(64) NOT NULL UNIQUE,
  role       VARCHAR(128),
  goal       TEXT,
  backstory  TEXT,
  enabled    BOOLEAN DEFAULT TRUE,
  builtin    BOOLEAN DEFAULT FALSE,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS reports (
  id           BIGINT PRIMARY KEY AUTO_INCREMENT,
  task_id      VARCHAR(64),
  service_name VARCHAR(128),
  report_type  VARCHAR(64),
  verdict      VARCHAR(24),
  risk_score   FLOAT,
  summary      TEXT,
  content      LONGTEXT,
  file_key     VARCHAR(255) NULL,
  created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_reports_svc_created (service_name, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS rules (
  id              BIGINT PRIMARY KEY AUTO_INCREMENT,
  rule_key        VARCHAR(64) NOT NULL UNIQUE,
  name            VARCHAR(128),
  kind            VARCHAR(32),
  severity        VARCHAR(16),
  enabled         BOOLEAN DEFAULT TRUE,
  scope_type      VARCHAR(32),
  join_mode       VARCHAR(8),
  conditions_json JSON,
  source_type     VARCHAR(32),
  created_by      VARCHAR(64),
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  deleted_at      DATETIME NULL,
  KEY idx_rules_enabled (enabled),
  KEY idx_rules_kind_enabled (kind, enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
