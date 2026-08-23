-- mysql/0003b-ai-runtime-recovery
-- V9.3 Phase 10 (P10 完整闭环, Plan C)：Plan/Step/Tool/Action 恢复结构。
-- owner: query-api Control Plane Persistence。
-- 语句用 "-- statement-breakpoint" 分隔。
-- P0-4 修正：幂等键（idempotency_key/event_id）先加列 + 回填唯一值，再建 UNIQUE，
-- 避免历史全 '' 行在唯一约束下重复失败。

-- 1) ai_plan_steps：补完整 DAG depends_on / 运行态 parameters/attempt/outcome/result_ref
ALTER TABLE ai_plan_steps
  ADD COLUMN depends_on JSON NULL,
  ADD COLUMN parameters JSON NULL,
  ADD COLUMN attempt INT NOT NULL DEFAULT 0,
  ADD COLUMN outcome VARCHAR(32) NULL,
  ADD COLUMN result_ref VARCHAR(512) NULL;
-- statement-breakpoint

-- 2) ai_tool_runs：补稳定幂等键 idempotency_key + 唯一约束（重启恢复不重复执行）
ALTER TABLE ai_tool_runs
  ADD COLUMN idempotency_key VARCHAR(255) NOT NULL DEFAULT '';
-- statement-breakpoint
UPDATE ai_tool_runs SET idempotency_key = CONCAT('legacy-', tool_run_id) WHERE idempotency_key = '' OR idempotency_key IS NULL;
-- statement-breakpoint
ALTER TABLE ai_tool_runs
  ADD UNIQUE KEY uq_ai_tool_runs_idem (run_id, idempotency_key);
-- statement-breakpoint

-- 3) ai_actions：idempotency_key 已存在列，回填唯一值后建唯一约束
UPDATE ai_actions SET idempotency_key = CONCAT('legacy-', action_id) WHERE idempotency_key = '' OR idempotency_key IS NULL;
-- statement-breakpoint
ALTER TABLE ai_actions
  ADD UNIQUE KEY uq_ai_actions_idem (run_id, idempotency_key);
-- statement-breakpoint

-- 4) ai_control_commands：control command 幂等持久化
CREATE TABLE IF NOT EXISTS ai_control_commands (
  command_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  operation VARCHAR(64) NOT NULL,
  payload_json JSON NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_control_commands_idem (run_id, idempotency_key),
  INDEX idx_ai_control_commands_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
