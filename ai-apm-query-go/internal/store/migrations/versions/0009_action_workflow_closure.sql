-- Durable Action workflow closure (expand-only).
-- Existing rows retain their legacy hash schema and nullable approval version;
-- new canonical Action V2 rows use hash_schema_version=2 and action_version>=1.

ALTER TABLE ai_actions
  ADD COLUMN hash_schema_version SMALLINT NOT NULL DEFAULT 1 AFTER action_hash,
  ADD COLUMN action_version BIGINT NOT NULL DEFAULT 1 AFTER hash_schema_version,
  ADD COLUMN proposed_by CHAR(36) NULL AFTER action_version,
  ADD COLUMN policy_version VARCHAR(64) NOT NULL DEFAULT 'action-policy-v1' AFTER proposed_by,
  ADD COLUMN preflight_status VARCHAR(32) NOT NULL DEFAULT 'unresolved' AFTER policy_version,
  ADD COLUMN target_resource_type VARCHAR(32) NOT NULL DEFAULT 'deployment' AFTER preflight_status,
  ADD UNIQUE KEY uq_ai_actions_id_version (action_id, action_version);
-- statement-breakpoint

ALTER TABLE ai_approval_decisions
  ADD COLUMN action_version BIGINT NULL AFTER action_hash,
  ADD COLUMN decision_idempotency_key VARCHAR(255) NULL AFTER action_version,
  ADD UNIQUE KEY uq_ai_approval_action_version (action_id, action_version),
  ADD UNIQUE KEY uq_ai_approval_decision_key (action_id, decision_idempotency_key);
-- statement-breakpoint

ALTER TABLE ai_plan_steps
  ADD COLUMN payload_hash CHAR(64) NOT NULL DEFAULT '' AFTER parameters;
-- statement-breakpoint

ALTER TABLE ai_hypotheses
  ADD COLUMN payload_hash CHAR(64) NOT NULL DEFAULT '' AFTER content;
-- statement-breakpoint

ALTER TABLE ai_verifications
  ADD COLUMN payload_hash CHAR(64) NOT NULL DEFAULT '' AFTER checks_json;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS ai_action_outbox (
  command_id CHAR(36) PRIMARY KEY,
  action_id CHAR(36) NOT NULL,
  action_version BIGINT NOT NULL,
  action_hash CHAR(64) NOT NULL,
  run_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'pending',
  dispatch_owner_id VARCHAR(255) NULL,
  dispatch_epoch BIGINT NOT NULL DEFAULT 0,
  dispatch_token_hash CHAR(64) NULL,
  dispatch_expires_at DATETIME(3) NULL,
  dispatch_count INT NOT NULL DEFAULT 0,
  next_retry_at DATETIME(3) NULL,
  delivered_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_action_outbox_action_version (action_id, action_version),
  INDEX idx_ai_action_outbox_pending (status, next_retry_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS ai_action_reconciliations (
  reconciliation_id CHAR(36) PRIMARY KEY,
  action_id CHAR(36) NOT NULL,
  attempt_id CHAR(36) NOT NULL,
  action_hash CHAR(64) NOT NULL,
  status VARCHAR(24) NOT NULL,
  observed_uid VARCHAR(128) NOT NULL DEFAULT '',
  observed_version VARCHAR(128) NOT NULL DEFAULT '',
  observed_json JSON NULL,
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_action_reconcile_attempt (attempt_id),
  INDEX idx_ai_action_reconcile_action (action_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- statement-breakpoint

CREATE INDEX idx_ai_runs_recovery
  ON ai_runs(status, lease_expires_at, retry_not_before, created_at, run_id);
