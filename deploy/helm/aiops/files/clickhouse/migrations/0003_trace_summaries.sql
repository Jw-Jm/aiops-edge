-- 0003-trace-summary-state
-- Trace 列表的预聚合 Summary/Index 层。trace_spans 仍是明细 SoT；本迁移不修改
-- DeepFlow 私有 ClickHouse，也不删除/改写明细数据。

ALTER TABLE observability.trace_spans
    ADD COLUMN IF NOT EXISTS `span_dedup_key` String DEFAULT '';

CREATE TABLE IF NOT EXISTS observability.trace_summary_state
(
    `tenant_id` String,
    `cluster_id` String,
    `date` Date,
    `trace_id` String,
    `start_state` AggregateFunction(min, DateTime64(9)),
    `end_state` AggregateFunction(max, DateTime64(9)),
    `span_count_state` AggregateFunction(uniqExact, String),
    `service_count_state` AggregateFunction(uniqExact, String),
    `max_duration_state` AggregateFunction(max, UInt64),
    `error_state` AggregateFunction(max, UInt8),
    `service_names_state` AggregateFunction(groupUniqArray, String),
    `operation_names_state` AggregateFunction(groupUniqArray, String),
    `http_urls_state` AggregateFunction(groupUniqArray, String)
)
ENGINE = AggregatingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, date, trace_id)
TTL date + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

CREATE TABLE IF NOT EXISTS observability.trace_summary_backfill_markers
(
    `tenant_id` String DEFAULT '',
    `date` Date,
    `completed_at` DateTime64(3),
    `version` UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id, date);

CREATE TABLE IF NOT EXISTS observability.trace_summary_index
(
    `tenant_id` String,
    `cluster_id` String,
    `date` Date,
    `time_bucket` DateTime,
    `trace_id` String,
    `latest_start` DateTime64(9),
    `service_name` String DEFAULT '',
    `search_text` String DEFAULT ''
)
ENGINE = MergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, date, time_bucket, latest_start, trace_id)
TTL toDateTime(latest_start) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

ALTER TABLE observability.trace_summary_index
    ADD COLUMN IF NOT EXISTS `service_name` String DEFAULT '';
ALTER TABLE observability.trace_summary_index
    ADD COLUMN IF NOT EXISTS `search_text` String DEFAULT '';

CREATE TABLE IF NOT EXISTS observability.trace_summary_index_backfill_markers
(
    `tenant_id` String DEFAULT '',
    `date` Date,
    `completed_at` DateTime64(3),
    `version` UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id, date);

CREATE MATERIALIZED VIEW IF NOT EXISTS observability.trace_spans_to_summary_state
TO observability.trace_summary_state
AS
SELECT
    tenant_id,
    cluster_id,
    date,
    trace_id,
    minState(start_time) AS start_state,
    maxState(start_time) AS end_state,
    uniqExactState(if(span_dedup_key = '', span_id, span_dedup_key)) AS span_count_state,
    uniqExactState(service_name) AS service_count_state,
    maxState(duration_ns) AS max_duration_state,
    maxState(is_error) AS error_state,
    groupUniqArrayState(service_name) AS service_names_state,
    groupUniqArrayState(operation_name) AS operation_names_state,
    groupUniqArrayState(http_url) AS http_urls_state
FROM observability.trace_spans
GROUP BY tenant_id, cluster_id, date, trace_id;

CREATE MATERIALIZED VIEW IF NOT EXISTS observability.trace_spans_to_summary_index
TO observability.trace_summary_index
AS
SELECT
    tenant_id,
    cluster_id,
    date,
    toStartOfFiveMinutes(start_time) AS time_bucket,
    trace_id,
    max(start_time) AS latest_start,
    service_name,
    arrayStringConcat(groupUniqArray(concat(operation_name, ' ', http_url)), ' ') AS search_text
FROM observability.trace_spans
GROUP BY tenant_id, cluster_id, date, time_bucket, trace_id, service_name;
