-- mysql/0002-ai-runtime
-- V9.2 Phase 4：AI Runtime 控制面表（owner: query-api Control Plane Persistence）。
-- 字段唯一来源：docs/AIOPS_DATA_MODEL_REDESIGN.md (frozen target)。
-- orchestrator 对这些表 DIRECT MYSQL ACCESS = FORBIDDEN（只经 query-api 内部控制面）。
-- 语句用 "-- statement-breakpoint" 分隔。

-- ai_runs：AI Run 主记录。scope_kind 区分单/多集群；primary_cluster_id 在 single 时=该集群、multi 时 NULL；
-- cluster membership 由 ai_run_clusters 表达（不用 cluster_id 空字符串表示 default）。
CREATE TABLE IF NOT EXISTS ai_runs (
  run_id CHAR(36) PRIMARY KEY,
  request_id CHAR(36) NOT NULL DEFAULT '',
  tenant_id CHAR(36) NOT NULL,
  principal VARCHAR(255) NOT NULL DEFAULT '',
  scope_kind VARCHAR(16) NOT NULL DEFAULT 'single_cluster',
  primary_cluster_id CHAR(36) NULL,
  intent VARCHAR(255) NOT NULL DEFAULT '',
  action_mode VARCHAR(32) NOT NULL DEFAULT '',
  target_type VARCHAR(32) NULL,
  target_resource_id VARCHAR(512) NULL,
  time_range_start DATETIME(3) NULL,
  time_range_end DATETIME(3) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  state_version BIGINT NOT NULL DEFAULT 0,
  parent_run_id CHAR(36) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT chk_ai_runs_scope_kind CHECK (scope_kind IN ('single_cluster','multi_cluster'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_run_clusters：multi-cluster run 成员（single_cluster 时恰 1 行 = primary；multi 时 >=2 行）
CREATE TABLE IF NOT EXISTS ai_run_clusters (
  run_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  PRIMARY KEY (run_id, cluster_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_plan_steps：DAG step。cluster_id 可 NULL（aggregate 计划步骤不绑单一集群）。
CREATE TABLE IF NOT EXISTS ai_plan_steps (
  step_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  parent_step_id CHAR(36) NULL,
  seq INT NOT NULL DEFAULT 0,
  step_type VARCHAR(32) NOT NULL DEFAULT '',
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  cluster_id CHAR(36) NULL,
  description TEXT NULL,
  budget_used INT NOT NULL DEFAULT 0,
  started_at DATETIME(3) NULL,
  completed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_ai_plan_steps_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_tool_runs：工具调用。cluster_id NOT NULL（绑定到具体集群执行）。
CREATE TABLE IF NOT EXISTS ai_tool_runs (
  tool_run_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  step_id CHAR(36) NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  tool_name VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  input_json JSON NULL,
  result_json JSON NULL,
  error_code VARCHAR(64) NULL,
  error_message TEXT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  started_at DATETIME(3) NULL,
  completed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  INDEX idx_ai_tool_runs_run (run_id),
  INDEX idx_ai_tool_runs_cluster (cluster_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_evidence：证据记录。大 payload 存 MinIO；本表存 raw_ref/digest/summary/metadata。
-- cluster_id NOT NULL；provenance_fingerprint 用于去重。
CREATE TABLE IF NOT EXISTS ai_evidence (
  evidence_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  evidence_type VARCHAR(64) NOT NULL DEFAULT '',
  source_ref VARCHAR(512) NOT NULL DEFAULT '',
  raw_ref VARCHAR(512) NOT NULL DEFAULT '',
  raw_digest_sha256 CHAR(64) NOT NULL DEFAULT '',
  summary TEXT NULL,
  metadata_json JSON NULL,
  provenance_fingerprint CHAR(64) NOT NULL DEFAULT '',
  collected_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  INDEX idx_ai_evidence_run (run_id),
  INDEX idx_ai_evidence_cluster (cluster_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_hypotheses：RCA 假设。cluster_id NOT NULL。
CREATE TABLE IF NOT EXISTS ai_hypotheses (
  hypothesis_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  content TEXT NOT NULL,
  confidence DOUBLE NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'proposed',
  confirmed_by_evidence TINYINT NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_ai_hypotheses_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_actions：写操作（可审批、可回滚）。cluster_id NOT NULL；action_hash + idempotency_key 保证幂等。
CREATE TABLE IF NOT EXISTS ai_actions (
  action_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  action_type VARCHAR(64) NOT NULL DEFAULT '',
  action_hash CHAR(64) NOT NULL DEFAULT '',
  idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
  proposed_risk VARCHAR(8) NOT NULL DEFAULT 'R0',
  authoritative_risk VARCHAR(8) NOT NULL DEFAULT 'R0',
  status VARCHAR(32) NOT NULL DEFAULT 'proposed',
  dry_run TINYINT NOT NULL DEFAULT 1,
  params_json JSON NULL,
  result_json JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_ai_actions_run (run_id),
  INDEX idx_ai_actions_cluster (cluster_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_verifications：验证快照/结果。cluster_id NOT NULL。
CREATE TABLE IF NOT EXISTS ai_verifications (
  verification_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  action_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  before_snapshot JSON NULL,
  after_snapshot JSON NULL,
  observation_window_seconds INT NOT NULL DEFAULT 120,
  checks_json JSON NULL,
  summary TEXT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_ai_verifications_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_approval_decisions：审批决定。cluster_id NOT NULL；绑定 action_hash。
CREATE TABLE IF NOT EXISTS ai_approval_decisions (
  approval_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  action_id CHAR(36) NOT NULL,
  action_hash CHAR(64) NOT NULL DEFAULT '',
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  decision VARCHAR(16) NOT NULL DEFAULT 'pending',
  approver VARCHAR(255) NOT NULL DEFAULT '',
  reason TEXT NULL,
  decided_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  INDEX idx_ai_approval_decisions_run (run_id),
  INDEX idx_ai_approval_decisions_action (action_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_run_events：run 事件序列（不可变追加）。PRIMARY KEY(run_id, sequence)。
CREATE TABLE IF NOT EXISTS ai_run_events (
  run_id CHAR(36) NOT NULL,
  sequence BIGINT NOT NULL,
  event_type VARCHAR(64) NOT NULL DEFAULT '',
  payload_json JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id, sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- ai_audit_events：AI Run 内业务审计。
CREATE TABLE IF NOT EXISTS ai_audit_events (
  audit_id BIGINT AUTO_INCREMENT PRIMARY KEY,
  run_id CHAR(36) NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NULL,
  request_id CHAR(36) NOT NULL DEFAULT '',
  actor VARCHAR(255) NOT NULL DEFAULT '',
  action VARCHAR(128) NOT NULL DEFAULT '',
  result VARCHAR(32) NOT NULL DEFAULT 'success',
  detail JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  INDEX idx_ai_audit_events_run (run_id),
  INDEX idx_ai_audit_events_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
