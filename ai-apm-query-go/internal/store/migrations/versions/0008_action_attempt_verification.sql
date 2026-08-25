-- V2 P0：动作执行 attempt / verification 持久化边界。
-- Query API/MySQL 是唯一 SoT；Action Executor 只返回结果，不自建状态。

CREATE TABLE IF NOT EXISTS ai_action_attempts (
  attempt_id CHAR(36) PRIMARY KEY,
  action_id CHAR(36) NOT NULL,
  run_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  idempotency_key VARCHAR(255) NOT NULL,
  action_hash CHAR(64) NOT NULL DEFAULT '',
  request_digest_sha256 CHAR(64) NOT NULL DEFAULT '',
  status VARCHAR(32) NOT NULL DEFAULT 'queued',
  executor_id VARCHAR(255) NOT NULL DEFAULT '',
  request_json JSON NULL,
  result_json JSON NULL,
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  started_at DATETIME(3) NULL,
  finished_at DATETIME(3) NULL,
  completed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  -- One executor attempt is allowed per immutable action/idempotency key. A
  -- response-loss retry must reconcile this row before any new mutation call.
  UNIQUE KEY uq_ai_action_attempt (action_id, attempt_id),
  UNIQUE KEY uq_ai_action_attempt_idem (action_id, idempotency_key),
  INDEX idx_ai_action_attempt_action (action_id, created_at),
  INDEX idx_ai_action_attempts_run (run_id, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
