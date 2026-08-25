-- =============================================================================
-- V2 P0 修复（0006）：Lease caller claim/token + Cancel lease invalidation + ToolRun run_id/args_hash。
--
-- P0-LEASE-03：ai_run_claims.claim_source（LIVE_INVOCATION|RECOVERY）审计。
-- P0-TOOL-01：ai_tool_runs.args_hash（Tool 幂等域 (run_id,idempotency_key,args_hash)）。
-- P0-TOOL-01：ai_tool_runs.run_id 完整性约束（禁止空串）。
-- P0#1（Public Cancel）：Cancel 原子 lease_epoch++ / clear lease（应用层在 CancelTx 完成；此处无 DDL）。
-- =============================================================================
ALTER TABLE ai_run_claims
    ADD COLUMN `claim_source` VARCHAR(32) NOT NULL DEFAULT 'LIVE_INVOCATION' AFTER `lease_token_hash`;

-- statement-breakpoint

ALTER TABLE ai_tool_runs
    ADD COLUMN `args_hash` CHAR(64) NULL AFTER `idempotency_key`;

-- statement-breakpoint

ALTER TABLE ai_tool_runs
    ADD CONSTRAINT `chk_ai_tool_runs_run_id_nonempty`
    CHECK (CHAR_LENGTH(`run_id`) > 0);
