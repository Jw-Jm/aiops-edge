-- =============================================================================
-- C-01（0004_runtime_convergence / 报告 §16）：固定 ClickHouse trace_spans 为平台
-- Trace Persistent SoT，并增加 span_dedup_key 幂等去重列。
--
-- 幂等语义：同一 Span 重复投递（OTLP/DeepFlow/重试）只形成一个逻辑 Trace/Evidence。
-- ClickHouse ReplacingMergeTree 的 ORDER BY 已含 span_id；增加 span_dedup_key 列用于
-- query 侧/ETL 去重断言，并保持 additive 向后兼容。
-- =============================================================================

ALTER TABLE observability.trace_spans
    ADD COLUMN IF NOT EXISTS `span_dedup_key` String DEFAULT '',
    ADD COLUMN IF NOT EXISTS `date_bucket` Date DEFAULT toDate(start_time);

-- 去重辅助索引（按 span_dedup_key 快速查重，非唯一约束——最终去重由 ReplacingMergeTree
-- ORDER BY + span_dedup_key 兜底，查询侧对同 dedup_key 只取一条）。
ALTER TABLE observability.trace_spans
    ADD INDEX IF NOT EXISTS idx_span_dedup (span_dedup_key) TYPE minmax GRANULARITY 4;
