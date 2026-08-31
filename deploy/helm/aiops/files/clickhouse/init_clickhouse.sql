-- =============================================================================
-- observability ClickHouse 初始化建库建表脚本
-- 用途: 任意环境 helm install 时自动建库建表（可移植，无环境写死）
-- 特点: 幂等(IF NOT EXISTS)、仅建结构、无任何历史数据
-- =============================================================================

CREATE DATABASE IF NOT EXISTS observability;

-- -----------------------------------------------------------------------------
-- log_records: 日志记录
-- TTL 30 天（与 alert_events 一致），防止无限膨胀打满磁盘
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.log_records
(
    `tenant_id` String,
    `cluster_id` String,
    `timestamp` DateTime64(9),
    `service_name` String,
    `severity` String,
    `body` String,
    `attributes` Map(String, String),
    `trace_id` String,
    `span_id` String,
    `time_bucket` DateTime,
    `date` Date
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, date, timestamp, trace_id)
TTL toDateTime(timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- service_topology: 服务拓扑边
-- TTL 30 天，防止无限膨胀
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.service_topology
(
    `tenant_id` String,
    `cluster_id` String,
    `source_service` String,
    `target_service` String,
    `time_bucket` DateTime,
    `call_count` UInt64,
    `error_count` UInt64,
    `avg_duration_ns` UInt64,
    `date` Date
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, source_service, target_service, date, time_bucket)
TTL toDateTime(time_bucket) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- change_records: 变更事实（changes.read / Graph ChangeBuilder 的 SoT）
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.change_records
(
    `tenant_id` String,
    `cluster_id` String,
    `change_id` String,
    `service_name` String,
    `change_type` String,
    `start_time` DateTime64(3),
    `actor` String,
    `summary` String,
    `revision` String,
    `date` Date DEFAULT toDate(start_time)
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, start_time, change_id)
TTL toDateTime(start_time) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- trace_spans: 调用链 span
-- TTL 30 天，防止无限膨胀
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.trace_spans
(
    `tenant_id` String,
    `cluster_id` String,
    `trace_id` String,
    `span_id` String,
    `parent_span_id` String,
    `service_name` String,
    `operation_name` String,
    `span_kind` String,
    `status_code` UInt8,
    `start_time` DateTime64(9),
    `duration_ns` UInt64,
    `attributes` Map(String, String),
    `http_method` String,
    `http_status_code` UInt16,
    `http_url` String,
    `db_system` String,
    `db_statement` String,
    `rpc_system` String,
    `service_instance_id` String,
    `k8s_namespace` String,
    `k8s_pod_name` String,
    `is_slow` UInt8,
    `is_error` UInt8,
    `time_bucket` DateTime,
    `date` Date,
    `span_dedup_key` String DEFAULT ''
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, date, start_time, span_id)
TTL toDateTime(start_time) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- trace_summary_state: Trace 列表摘要/索引
-- 由 AggregatingMergeTree + 物化视图增量维护；列表查询不得回到 trace_spans 全量聚合。
-- -----------------------------------------------------------------------------
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

-- Lightweight candidate index. It is intentionally append-only and ordered by
-- time so the list query can find recent Trace IDs before touching aggregate
-- states. It is not a second Span SoT and is never used for Trace details.
CREATE TABLE IF NOT EXISTS observability.trace_summary_index
(
    `tenant_id` String,
    `cluster_id` String,
    `date` Date,
    `time_bucket` DateTime,
    `trace_id` String,
    `latest_start` DateTime64(9),
    `latest_start_key` Int64,
    `service_name` String DEFAULT '',
    `search_text` String DEFAULT ''
)
ENGINE = MergeTree
PARTITION BY date
ORDER BY (tenant_id, date, latest_start_key, cluster_id, trace_id, service_name)
TTL toDateTime(latest_start) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

ALTER TABLE observability.trace_summary_index
    ADD COLUMN IF NOT EXISTS `service_name` String DEFAULT '';
ALTER TABLE observability.trace_summary_index
    ADD COLUMN IF NOT EXISTS `search_text` String DEFAULT '';
ALTER TABLE observability.trace_summary_index
    ADD COLUMN IF NOT EXISTS `latest_start_key` Int64 DEFAULT 0;

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
    -toUnixTimestamp64Nano(max(start_time)) AS latest_start_key,
    service_name,
    arrayStringConcat(groupUniqArray(concat(operation_name, ' ', http_url)), ' ') AS search_text
FROM observability.trace_spans
GROUP BY tenant_id, cluster_id, date, time_bucket, trace_id, service_name;

-- -----------------------------------------------------------------------------
-- alert_events: 告警事件（大容量场景用 CH 列式存储；query-api 为写入者）
-- ReplacingMergeTree(version) 按 id 去重并保留最新状态；TTL 管理生命周期
-- 替代原 MySQL alert_events + 内存态 maxAlertEvents=1000 手工裁剪
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.alert_events
(
    `id` String,
    `tenant_id` String,
    `cluster_id` String,
    `rule_id` String,
    `rule_name` String,
    `service` String,
    `severity` String,
    `message` String,
    `value` Float64,
    `threshold` Float64,
    `timestamp` DateTime64(3),
    `count` UInt32,
    `first_timestamp` DateTime64(3),
    `last_timestamp` DateTime64(3),
    `status` String,
    `acknowledged_at` DateTime64(3),
    `acknowledged_by` String,
    `resolved_at` DateTime64(3),
    `resolved_by` String,
    `timeline` String,
    `investigation` String,
    `signature` String,
    `version` UInt64,
    `date` Date
)
ENGINE = ReplacingMergeTree(version)
PARTITION BY (tenant_id, toYYYYMM(timestamp))
ORDER BY (tenant_id, cluster_id, timestamp, id)
TTL toDateTime(last_timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- k8s_events: Kubernetes/IPMI events emitted by event-collector
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.k8s_events
(
    `tenant_id` String,
    `cluster_id` String,
    `ts` DateTime64(9),
    `namespace` String,
    `kind` String,
    `name` String,
    `reason` String,
    `type` String,
    `message` String,
    `involved_object` String,
    `source_component` String,
    `source` String,
    `node` String DEFAULT '',
    `time_bucket` DateTime,
    `event_id` String DEFAULT ''
)
ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, cluster_id, event_id)
TTL time_bucket + INTERVAL 30 DAY;
