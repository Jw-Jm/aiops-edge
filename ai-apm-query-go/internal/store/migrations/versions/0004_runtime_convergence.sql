-- mysql/0004-runtime-convergence
-- 生产收敛（A0 起）：ai_runs Lease/Runtime 元数据 + Runtime Commit + Run Claim +
-- Context Replay Guard + Control Command 幂等 + ToolRun 数据质量 + Outbox dispatch fencing。
-- owner: query-api Control Plane Persistence（schema-migrator 唯一 DDL owner）。
-- 语句用 "-- statement-breakpoint" 分隔。
-- 0004 必须 additive/backward-compatible：新二进制要求 0004；旧二进制只校验 0001~0003b。
-- 本文件一次成型（checksum 冻结），A0 仅使用 control_commands 相关列，其余列留待 A1/B1。

-- 1) ai_runs：Run execution Lease + Runtime 等待元数据（正交化，不新增第二套 RunStatus）
ALTER TABLE ai_runs
  ADD COLUMN lease_owner_id VARCHAR(128) NULL,
  ADD COLUMN lease_epoch BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN lease_claim_id CHAR(36) NULL,
  ADD COLUMN lease_token_hash CHAR(64) NULL,
  ADD COLUMN lease_expires_at DATETIME(3) NULL,
  ADD COLUMN heartbeat_at DATETIME(3) NULL,
  ADD COLUMN runtime_wait_kind VARCHAR(32) NOT NULL DEFAULT 'none',
  ADD COLUMN retry_not_before DATETIME(3) NULL,
  ADD COLUMN retry_attempt INT NOT NULL DEFAULT 0,
  ADD COLUMN last_failure_code VARCHAR(64) NULL,
  ADD COLUMN runtime_metadata_json JSON NULL;
-- statement-breakpoint

-- 2) ai_runtime_commits：Runtime Commit 幂等记录（响应丢失后返回首次结果）
CREATE TABLE IF NOT EXISTS ai_runtime_commits (
  run_id CHAR(36) NOT NULL,
  commit_id CHAR(36) NOT NULL,
  payload_hash CHAR(64) NOT NULL,
  committed_state_version BIGINT NOT NULL,
  result_status VARCHAR(32) NOT NULL,
  first_event_sequence BIGINT NULL,
  last_event_sequence BIGINT NULL,
  response_json JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id, commit_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- 3) ai_run_claims：Run execution lease claim 历史
CREATE TABLE IF NOT EXISTS ai_run_claims (
  run_id CHAR(36) NOT NULL,
  claim_id CHAR(36) NOT NULL,
  executor_id VARCHAR(128) NOT NULL,
  lease_epoch BIGINT NOT NULL,
  lease_token_hash CHAR(64) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id, claim_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- 4) ai_context_replay_guard：共享 Context 防重放（一次性消费）
CREATE TABLE IF NOT EXISTS ai_context_replay_guard (
  issuer VARCHAR(128) NOT NULL,
  audience VARCHAR(128) NOT NULL,
  nonce CHAR(36) NOT NULL,
  request_hash CHAR(64) NULL,
  consumed_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at DATETIME(3) NOT NULL,
  PRIMARY KEY (issuer, audience, nonce),
  INDEX idx_replay_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

-- 5) ai_control_commands：补 Control Command 真正幂等（payload hash + stored response + completed_at）
ALTER TABLE ai_control_commands
  ADD COLUMN payload_hash CHAR(64) NULL,
  ADD COLUMN response_json JSON NULL,
  ADD COLUMN completed_at DATETIME(3) NULL;
-- statement-breakpoint

-- 6) ai_tool_runs：数据质量/幂等/Lease 绑定字段
ALTER TABLE ai_tool_runs
  ADD COLUMN args_hash CHAR(64) NULL,
  ADD COLUMN executor_id VARCHAR(128) NULL,
  ADD COLUMN lease_epoch_at_start BIGINT NULL,
  ADD COLUMN deadline_at DATETIME(3) NULL,
  ADD COLUMN observed_at DATETIME(3) NULL,
  ADD COLUMN query_window_start DATETIME(3) NULL,
  ADD COLUMN query_window_end DATETIME(3) NULL,
  ADD COLUMN result_quality VARCHAR(16) NOT NULL DEFAULT 'none',
  ADD COLUMN result_complete TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN result_truncated TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN result_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN result_digest_sha256 CHAR(64) NULL,
  ADD COLUMN eligible_for_evidence TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN evidence_consumed_at DATETIME(3) NULL;
-- statement-breakpoint

-- 7) ai_run_outbox：dispatch fencing（与 Run execution lease 分离）
ALTER TABLE ai_run_outbox
  ADD COLUMN dispatch_owner_id VARCHAR(128) NULL,
  ADD COLUMN dispatch_epoch BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN dispatch_token_hash CHAR(64) NULL,
  ADD COLUMN dispatch_expires_at DATETIME(3) NULL,
  ADD COLUMN delivered_at DATETIME(3) NULL,
  ADD INDEX idx_run_outbox_dispatch (status, next_retry_at, dispatch_expires_at, created_at);
