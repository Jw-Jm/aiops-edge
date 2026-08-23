-- mysql/0003-ai-runtime-v2
-- V9.3 Phase 10 (P10 完整闭环, Plan A/B)：幂等 / 序列 / 派发结构。
-- owner: query-api Control Plane Persistence。
-- 语句用 "-- statement-breakpoint" 分隔。

-- 1) 历史空 request_id 回填（避免 UNIQUE(tenant_id, request_id) 同租户重复空值失败）
UPDATE ai_runs SET request_id = CONCAT('legacy-', run_id) WHERE request_id = '' OR request_id IS NULL;
-- statement-breakpoint

-- 2) ai_runs：补权威 Run 字段 + 幂等约束 + sequence counter + finished_at
--   注意：target_type/target_resource_id/time_range_start/time_range_end 已在 0002 定义，
--   这里**只加 0002 缺失的列**，否则整条多列 ALTER 因 1060（重复列）被幂等吞掉回滚，
--   principal_type 等新列将永远不生效（真实 MySQL 集成测试暴露的迁移 bug，P0-4 修复）。
ALTER TABLE ai_runs
  ADD COLUMN principal_type VARCHAR(32) NOT NULL DEFAULT 'user',
  ADD COLUMN session_id CHAR(36) NULL,
  ADD COLUMN finished_at DATETIME(3) NULL,
  ADD COLUMN last_event_sequence BIGINT NOT NULL DEFAULT 0,
  ADD UNIQUE KEY uq_ai_runs_tenant_request (tenant_id, request_id);
-- statement-breakpoint

-- 状态起点统一为 created（对齐 orchestrator contracts.RunStatus.CREATED）
UPDATE ai_runs SET status = 'created' WHERE status = 'pending';
-- statement-breakpoint

-- 3) ai_run_events：event_id + 幂等（响应丢失重试不追加第二条）
-- P0-4 修正：先加列，**回填**每行唯一 event_id（避免全 '' 撞 UNIQUE），再加唯一约束。
ALTER TABLE ai_run_events
  ADD COLUMN event_id CHAR(36) NOT NULL DEFAULT '';
-- statement-breakpoint
UPDATE ai_run_events SET event_id = LOWER(HEX(RANDOM_BYTES(16))) WHERE event_id = '' OR event_id IS NULL;
-- statement-breakpoint
ALTER TABLE ai_run_events
  ADD UNIQUE KEY uq_ai_run_events_idem (run_id, event_id);
-- statement-breakpoint

-- 4) ai_run_outbox：创建后可靠派发（durable outbox / pull-claim）
CREATE TABLE IF NOT EXISTS ai_run_outbox (
  invocation_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending|claimed|delivered|expired
  dispatch_count INT NOT NULL DEFAULT 0,
  next_retry_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_ai_run_outbox_pending (status, next_retry_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
