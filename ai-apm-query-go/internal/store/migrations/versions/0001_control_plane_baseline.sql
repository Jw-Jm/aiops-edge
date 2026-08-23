-- mysql/0001-control-plane-baseline
-- V9.2 Phase 4：控制面 baseline + 空环境 legacy runtime 兼容表。
-- 权威迁移元数据表 aiops_schema_migrations 由 migrator 建立，不在本文件建。
-- 语句由 migrator 按专用分隔标记切分（该标记不在此注释中出现）。

-- ============ TARGET_TABLES（控制面目标表） ============

-- 用户（Identity/RBAC，query-api 物理写入者）
CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  username VARCHAR(128) NOT NULL UNIQUE,
  display_name VARCHAR(255) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  scope VARCHAR(512) NOT NULL DEFAULT '',
  is_approver TINYINT NOT NULL DEFAULT 0,
  user_uuid CHAR(36) NULL UNIQUE,
  role VARCHAR(64) NOT NULL DEFAULT '',
  active TINYINT NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- 会话（Phase 3 冻结表名 auth_sessions，非 user_sessions）
CREATE TABLE IF NOT EXISTS auth_sessions (
  session_id CHAR(36) PRIMARY KEY,
  user_uuid CHAR(36) NOT NULL,
  token_version BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at DATETIME(3) NULL,
  revoked_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ============ LEGACY_RUNTIME_REQUIRED_TABLES ============
-- 旧 orchestrator runtime 空环境启动仍依赖（P0-F）：标 LEGACY，PRESERVE / NO PHYSICAL DELETE，
-- 非最终 SoT。随对应 legacy 业务 path（Phase 14）退出。
CREATE TABLE IF NOT EXISTS approval_tasks (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  task_id VARCHAR(64) NOT NULL UNIQUE,
  service_name VARCHAR(128),
  status VARCHAR(24),
  plan TEXT,
  script TEXT,
  risk_score FLOAT,
  risk_reason TEXT,
  diagnosis TEXT,
  report TEXT,
  requester VARCHAR(64),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  decided_at DATETIME NULL,
  decision_by VARCHAR(64) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS reports (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  task_id VARCHAR(64),
  service_name VARCHAR(128),
  report_type VARCHAR(64),
  verdict VARCHAR(24),
  risk_score FLOAT,
  summary TEXT,
  content LONGTEXT,
  file_key VARCHAR(255) NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS change_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  cluster_id VARCHAR(64) NOT NULL DEFAULT 'default',
  service VARCHAR(255) NOT NULL,
  change_type VARCHAR(32) NOT NULL,
  operator VARCHAR(128) NOT NULL,
  content TEXT NOT NULL,
  related_trace_ids TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ============ 控制面业务表（EnsureSchema 迁入，DDL authority 接管） ============

-- users 兼容补列（EnsureSchema mysql.go:145-158 含 email/status）
ALTER TABLE users ADD COLUMN email VARCHAR(128) DEFAULT '';
-- statement-breakpoint
ALTER TABLE users ADD COLUMN status TINYINT DEFAULT 1;
-- statement-breakpoint

-- service_catalog 服务目录
CREATE TABLE IF NOT EXISTS service_catalog (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  service_name VARCHAR(128) NOT NULL UNIQUE,
  display_name VARCHAR(128) DEFAULT '',
  description TEXT,
  owner VARCHAR(128) DEFAULT '',
  team VARCHAR(128) DEFAULT '',
  tags VARCHAR(255) DEFAULT '',
  status ENUM('active','maintenance','deprecated') DEFAULT 'active',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- devices 设备
CREATE TABLE IF NOT EXISTS devices (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  hostname VARCHAR(128) NOT NULL UNIQUE,
  ip VARCHAR(64) DEFAULT '',
  os VARCHAR(64) DEFAULT '',
  cpu_cores INT DEFAULT 0,
  memory_mb BIGINT DEFAULT 0,
  status ENUM('online','offline','maintenance') DEFAULT 'online',
  role VARCHAR(64) DEFAULT '',
  location VARCHAR(128) DEFAULT '',
  tags VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- clusters 集群（基础表，authority 列由下方 ALTER 追加）
CREATE TABLE IF NOT EXISTS clusters (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL UNIQUE,
  provider VARCHAR(64) DEFAULT '',
  region VARCHAR(64) DEFAULT '',
  version VARCHAR(64) DEFAULT '',
  node_count INT DEFAULT 0,
  status ENUM('active','degraded','down') DEFAULT 'active',
  api_server VARCHAR(255) DEFAULT '',
  kubeconfig TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- clusters authority 列（ensureClusterAuthorityMetadata 迁入）
ALTER TABLE clusters ADD COLUMN cluster_id CHAR(36) NULL;
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN tenant_id VARCHAR(64) NULL;
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN slug VARCHAR(128) NULL;
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN environment VARCHAR(64) NOT NULL DEFAULT '';
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN credential_ref VARCHAR(512) NOT NULL DEFAULT '';
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN lifecycle_status VARCHAR(32) NOT NULL DEFAULT 'registered';
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN type VARCHAR(64) NOT NULL DEFAULT '';
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN capabilities VARCHAR(512) NOT NULL DEFAULT '';
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN labels TEXT;
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN deleted_at DATETIME NULL;
-- statement-breakpoint
ALTER TABLE clusters ADD COLUMN kubernetes_identity_uid VARCHAR(128) NULL;
-- statement-breakpoint
ALTER TABLE clusters ADD UNIQUE INDEX uq_clusters_cluster_id (cluster_id);
-- statement-breakpoint
ALTER TABLE clusters ADD UNIQUE INDEX uq_clusters_slug (slug);
-- statement-breakpoint

-- topology_nodes 拓扑顶点（typed property graph）
CREATE TABLE IF NOT EXISTS topology_nodes (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  type VARCHAR(32) NOT NULL,
  name VARCHAR(255) NOT NULL,
  props_json TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_nodes_type_name (type, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- topology_relations 拓扑有向边（src→dst, type 唯一）
CREATE TABLE IF NOT EXISTS topology_relations (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  src_id BIGINT NOT NULL,
  dst_id BIGINT NOT NULL,
  type VARCHAR(64) NOT NULL,
  props_json TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_relations_src_dst_type (src_id, dst_id, type),
  KEY idx_relations_src_type (src_id, type),
  KEY idx_relations_dst_type (dst_id, type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- topology_node_types 节点类型目录
CREATE TABLE IF NOT EXISTS topology_node_types (
  name VARCHAR(32) PRIMARY KEY,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  display_name_en VARCHAR(128) NOT NULL DEFAULT '',
  builtin TINYINT NOT NULL DEFAULT 0,
  tier INT NOT NULL DEFAULT 99,
  description TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- topology_relation_types 关系类型目录
CREATE TABLE IF NOT EXISTS topology_relation_types (
  name VARCHAR(64) PRIMARY KEY,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  display_name_en VARCHAR(128) NOT NULL DEFAULT '',
  builtin TINYINT NOT NULL DEFAULT 0,
  propagates_failure TINYINT NOT NULL DEFAULT 0,
  direction VARCHAR(16) NOT NULL DEFAULT 'src_to_dst',
  semantics_tag VARCHAR(32) NOT NULL DEFAULT '',
  description TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- platform_settings 平台设置（KV，config_key 规避保留字）
CREATE TABLE IF NOT EXISTS platform_settings (
  config_key VARCHAR(128) PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- llm_providers LLM Provider
CREATE TABLE IF NOT EXISTS llm_providers (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(64) DEFAULT 'openai_compatible',
  base_url VARCHAR(255) NOT NULL DEFAULT '',
  default_model VARCHAR(128) DEFAULT '',
  cost VARCHAR(64) DEFAULT '人民币',
  available TINYINT DEFAULT 1,
  enabled TINYINT DEFAULT 0,
  api_key_hash VARCHAR(64) DEFAULT '',
  api_key_encrypted TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- llm_config_history LLM 配置历史
CREATE TABLE IF NOT EXISTS llm_config_history (
  version BIGINT PRIMARY KEY,
  provider VARCHAR(128) DEFAULT '',
  model VARCHAR(128) DEFAULT '',
  base_url VARCHAR(255) DEFAULT '',
  api_key_hash VARCHAR(64) DEFAULT '',
  operator VARCHAR(128) DEFAULT '',
  comment VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- alert_rules 告警规则
CREATE TABLE IF NOT EXISTS alert_rules (
  id VARCHAR(32) PRIMARY KEY,
  name VARCHAR(255) NOT NULL DEFAULT '',
  service VARCHAR(128) DEFAULT '',
  type VARCHAR(32) DEFAULT 'threshold',
  metric VARCHAR(64) DEFAULT '',
  cond VARCHAR(8) DEFAULT '>',
  threshold DOUBLE DEFAULT 0,
  duration INT DEFAULT 5,
  severity VARCHAR(16) DEFAULT 'warning',
  enabled TINYINT DEFAULT 1,
  webhook_url VARCHAR(512) DEFAULT '',
  cooldown INT DEFAULT 0,
  dampening INT DEFAULT 0,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- alert_rules 扩展列（EnsureSchema 幂等 ALTER 迁入）
ALTER TABLE alert_rules ADD COLUMN baseline_seconds INT DEFAULT 900;
-- statement-breakpoint
ALTER TABLE alert_rules ADD COLUMN anomaly_method VARCHAR(16) DEFAULT 'zscore';
-- statement-breakpoint
ALTER TABLE alert_rules ADD COLUMN slo_id VARCHAR(64) DEFAULT '';
-- statement-breakpoint
ALTER TABLE alert_rules ADD COLUMN keyword VARCHAR(255) DEFAULT '';
-- statement-breakpoint
ALTER TABLE alert_rules ADD COLUMN cluster VARCHAR(64) DEFAULT '';
-- statement-breakpoint

-- slo_targets SLO 目标
CREATE TABLE IF NOT EXISTS slo_targets (
  id VARCHAR(64) PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  service VARCHAR(128) NOT NULL,
  slo_type VARCHAR(32) DEFAULT 'availability',
  target DECIMAL(10,4) NOT NULL DEFAULT 99.9,
  window_seconds INT DEFAULT 2592000,
  enabled TINYINT DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- dashboard_panels Monitor 看板面板
CREATE TABLE IF NOT EXISTS dashboard_panels (
  id VARCHAR(64) PRIMARY KEY,
  title VARCHAR(128) NOT NULL,
  query TEXT,
  chart_type VARCHAR(32) DEFAULT 'line',
  grid_x INT DEFAULT 0,
  grid_y INT DEFAULT 0,
  grid_w INT DEFAULT 6,
  grid_h INT DEFAULT 4,
  span INT DEFAULT 6,
  sort INT DEFAULT 0,
  enabled TINYINT DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- alert_silences 告警静默
CREATE TABLE IF NOT EXISTS alert_silences (
  id VARCHAR(32) PRIMARY KEY,
  service VARCHAR(128) DEFAULT '',
  rule_id VARCHAR(32) DEFAULT '',
  comment VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT NULL,
  expires_at DATETIME DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- tenants 租户
CREATE TABLE IF NOT EXISTS tenants (
  id VARCHAR(64) PRIMARY KEY,
  name VARCHAR(255) NOT NULL DEFAULT '',
  quota_ai INT DEFAULT 0,
  enabled TINYINT DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- service_metadata 服务富化元数据
CREATE TABLE IF NOT EXISTS service_metadata (
  service_name VARCHAR(255) PRIMARY KEY,
  owner VARCHAR(255) DEFAULT '',
  team VARCHAR(255) DEFAULT '',
  tier ENUM('critical','important','standard','experimental') DEFAULT 'standard',
  description TEXT,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_tier (tier)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- anomaly_events 异常检测持久化
CREATE TABLE IF NOT EXISTS anomaly_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  service_name VARCHAR(255) NOT NULL,
  metric VARCHAR(64) NOT NULL,
  value DOUBLE,
  method VARCHAR(32),
  severity VARCHAR(32),
  score DOUBLE,
  detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_service (service_name),
  INDEX idx_detected (detected_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- audit_logs 审计日志（LEGACY：V9.2 由 platform_audit_events/ai_audit_events 取代）
CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  task_id VARCHAR(64) DEFAULT '',
  action VARCHAR(128) NOT NULL DEFAULT '',
  operator VARCHAR(128) DEFAULT '',
  target_service VARCHAR(255) DEFAULT '',
  command VARCHAR(255) DEFAULT '',
  result VARCHAR(32) DEFAULT 'success',
  detail JSON NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_audit_created (created_at),
  INDEX idx_audit_operator (operator)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ============ RBAC 表（authorizationSchemaStatements 迁入） ============

CREATE TABLE IF NOT EXISTS user_tenants (
  user_uuid CHAR(36) NOT NULL,
  tenant_id VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_uuid, tenant_id),
  INDEX idx_user_tenants_tenant_status (tenant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS roles (
  role_id CHAR(36) PRIMARY KEY,
  name VARCHAR(128) NOT NULL UNIQUE,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS permissions (
  permission_id CHAR(36) PRIMARY KEY,
  action VARCHAR(128) NOT NULL UNIQUE,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS user_roles (
  user_uuid CHAR(36) NOT NULL,
  tenant_id VARCHAR(64) NOT NULL,
  role_id CHAR(36) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_uuid, tenant_id, role_id),
  INDEX idx_user_roles_lookup (user_uuid, tenant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS role_permissions (
  role_id CHAR(36) NOT NULL,
  permission_id CHAR(36) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS scope_assignments (
  scope_id BIGINT PRIMARY KEY AUTO_INCREMENT,
  role_id CHAR(36) NOT NULL,
  tenant_id VARCHAR(64) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  namespace VARCHAR(253) NOT NULL,
  resource_type VARCHAR(128) NOT NULL,
  resource_name VARCHAR(253) NOT NULL,
  action VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_scope_assignments_lookup (tenant_id, cluster_id, action, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS tenant_clusters (
  tenant_id VARCHAR(64) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (tenant_id, cluster_id),
  UNIQUE KEY uq_tenant_clusters_cluster (cluster_id),
  INDEX idx_tenant_clusters_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ============ Schema backfill DML（一次性历史修正，类别2） ============

UPDATE users SET user_uuid=LOWER(UUID()) WHERE user_uuid IS NULL OR user_uuid='';
-- statement-breakpoint

UPDATE clusters SET cluster_id=LOWER(UUID()) WHERE cluster_id IS NULL OR cluster_id='';
-- statement-breakpoint

UPDATE clusters SET slug=CONCAT('legacy-', id) WHERE slug IS NULL OR slug='';
-- statement-breakpoint

UPDATE clusters SET lifecycle_status=CASE status WHEN 'active' THEN 'ready' WHEN 'degraded' THEN 'degraded' WHEN 'down' THEN 'disabled' ELSE 'registered' END WHERE lifecycle_status='registered';
-- statement-breakpoint

INSERT IGNORE INTO service_metadata (service_name, owner, team, description)
SELECT service_name, owner, team, description FROM service_catalog
WHERE service_name IS NOT NULL AND service_name != '';

