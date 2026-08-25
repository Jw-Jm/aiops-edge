-- =============================================================================
-- V2 P0 接线（0007）：ai_actions 增加 executor 执行所需字段 + 执行结果持久化。
--
-- Stage D 接线：query-api 作为 executor 唯一调用方，将已批准 action 转换为
-- ActionExecutionContext，用 query-api 私钥签发 signed context 后转发到
-- ai-action-executor /v1/executor/execute，并把 executor 返回的 ActionResult
-- 持久化回 ai_actions（durable idempotency，重启恢复不重复执行）。
--
-- 新增字段：
--   target_name       K8s 目标 name（executor lookup 用；与 cluster_id 构成目标）
--   target_uid        批准时目标 UID（executor 执行前 TOCTOU reread 比对）
--   resource_version  批准时目标 resourceVersion（TOCTOU precondition）
--   namespace         目标 namespace
--   operation         白名单操作（patch/scale/restart）
--   execution_status  proposed|approved|queued|executing|success|failed|execution_unknown|rejected|rollback_required
--   executed_at       实际执行时间
--   error_code        结构化错误码（如 EXECUTOR_REJECTED / EXECUTION_UNKNOWN）
-- =============================================================================
ALTER TABLE ai_actions
    ADD COLUMN `target_name` VARCHAR(255) NOT NULL DEFAULT '' AFTER `action_type`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `target_uid` VARCHAR(128) NOT NULL DEFAULT '' AFTER `target_name`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `resource_version` VARCHAR(128) NOT NULL DEFAULT '' AFTER `target_uid`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `namespace` VARCHAR(128) NOT NULL DEFAULT '' AFTER `resource_version`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `operation` VARCHAR(32) NOT NULL DEFAULT '' AFTER `namespace`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `execution_status` VARCHAR(32) NOT NULL DEFAULT 'proposed' AFTER `status`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `executed_at` DATETIME(3) NULL AFTER `result_json`;

-- statement-breakpoint

ALTER TABLE ai_actions
    ADD COLUMN `error_code` VARCHAR(64) NOT NULL DEFAULT '' AFTER `executed_at`;
