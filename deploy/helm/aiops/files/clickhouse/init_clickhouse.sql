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
    `cluster_id` String DEFAULT 'default',
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
    `cluster_id` String DEFAULT 'default',
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
-- trace_spans: 调用链 span
-- TTL 30 天，防止无限膨胀
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.trace_spans
(
    `tenant_id` String,
    `cluster_id` String DEFAULT 'default',
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
    `date` Date
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, date, start_time, span_id)
TTL toDateTime(start_time) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- alert_events: 告警事件（大容量场景用 CH 列式存储；query-api 为写入者）
-- ReplacingMergeTree(version) 按 id 去重并保留最新状态；TTL 管理生命周期
-- 替代原 MySQL alert_events + 内存态 maxAlertEvents=1000 手工裁剪
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.alert_events
(
    `id` String,
    `cluster_id` String DEFAULT 'default',
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
PARTITION BY date
ORDER BY (service, rule_id, id)
TTL toDateTime(last_timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- k8s_events: Kubernetes/IPMI events emitted by event-collector
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.k8s_events
(
    `tenant_id` String,
    `cluster_id` String DEFAULT 'default',
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
    `time_bucket` DateTime
)
ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, cluster_id, ts, involved_object, reason, name, message)
TTL time_bucket + INTERVAL 30 DAY;
