-- =============================================================================
-- observability ClickHouse 初始化建库建表脚本
-- 来源: 192.168.0.63 环境 SHOW CREATE TABLE 真实表结构（只读导出）
-- 用途: 本机 OrbStack K8s 及任意环境 helm install 时自动建库建表
-- 特点: 幂等(IF NOT EXISTS)、仅建结构、无任何历史数据、与 63 环境解耦
-- =============================================================================

CREATE DATABASE IF NOT EXISTS observability;

-- -----------------------------------------------------------------------------
-- log_records: 日志记录
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.log_records
(
    `tenant_id` String,
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
ORDER BY (tenant_id, service_name, date, timestamp, trace_id)
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- service_topology: 服务拓扑边
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.service_topology
(
    `tenant_id` String,
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
ORDER BY (tenant_id, source_service, target_service, date, time_bucket)
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- trace_spans: 调用链 span
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.trace_spans
(
    `tenant_id` String,
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
ORDER BY (tenant_id, service_name, date, start_time, span_id)
SETTINGS index_granularity = 8192;

-- -----------------------------------------------------------------------------
-- alert_events: 告警事件（大容量场景用 CH 列式存储；query-api 为写入者）
-- ReplacingMergeTree(version) 按 id 去重并保留最新状态；TTL 管理生命周期
-- 替代原 MySQL alert_events + 内存态 maxAlertEvents=1000 手工裁剪
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observability.alert_events
(
    `id` String,
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
