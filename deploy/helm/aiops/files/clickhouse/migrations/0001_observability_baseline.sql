-- 0001-observability-baseline
-- V9.2 Phase 4 P4.5：ClickHouse observability 版本化 bootstrap（单一 DDL authority）。
-- 迁移元数据表 observability.aiops_schema_migrations 记录 migration_id/checksum/applied_at。
-- Raw Logs 只进 VictoriaLogs；log_records 完整副本标 LEGACY（不物理删除）。

CREATE DATABASE IF NOT EXISTS observability;

CREATE TABLE IF NOT EXISTS observability.aiops_schema_migrations
(
    migration_id String,
    checksum String,
    applied_at DateTime DEFAULT now(),
    PRIMARY KEY (migration_id)
) ENGINE = MergeTree
ORDER BY migration_id;

-- =============================================================================
-- log_records: LEGACY —— Raw Logs 完整副本。
-- V9.2 要求 Raw Logs 只进 VictoriaLogs。本表保留不删（不物理删除旧数据），
-- 但新写入不得再产生完整副本（Phase 5 writer 收敛）。TTL 30 天。
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.log_records
(
    tenant_id String,
    cluster_id String DEFAULT 'default',
    timestamp DateTime64(9),
    service_name String,
    severity String,
    body String,
    attributes Map(String, String),
    trace_id String,
    span_id String,
    time_bucket DateTime,
    date Date
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, date, timestamp, trace_id)
TTL toDateTime(timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- service_topology: 服务拓扑边（TTL 30 天）
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.service_topology
(
    tenant_id String,
    cluster_id String DEFAULT 'default',
    source_service String,
    target_service String,
    time_bucket DateTime,
    call_count UInt64,
    error_count UInt64,
    avg_duration_ns UInt64,
    date Date
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, source_service, target_service, date, time_bucket)
TTL toDateTime(time_bucket) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- change_records: 变更事实（changes.read / Graph ChangeBuilder 的 SoT）
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.change_records
(
    tenant_id String,
    cluster_id String DEFAULT 'default',
    change_id String,
    service_name String,
    change_type String,
    start_time DateTime64(3),
    actor String,
    summary String,
    revision String,
    date Date DEFAULT toDate(start_time)
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, start_time, change_id)
TTL toDateTime(start_time) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- trace_spans: 调用链 span（TTL 30 天）
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.trace_spans
(
    tenant_id String,
    cluster_id String DEFAULT 'default',
    trace_id String,
    span_id String,
    parent_span_id String,
    service_name String,
    operation_name String,
    span_kind String,
    status_code UInt8,
    start_time DateTime64(9),
    duration_ns UInt64,
    attributes Map(String, String),
    http_method String,
    http_status_code UInt16,
    http_url String,
    db_system String,
    db_statement String,
    rpc_system String,
    service_instance_id String,
    k8s_namespace String,
    k8s_pod_name String,
    is_slow UInt8,
    is_error UInt8,
    time_bucket DateTime,
    date Date
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, service_name, date, start_time, span_id)
TTL toDateTime(start_time) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- alert_events: 告警事件（query-api 为写入者；ReplacingMergeTree(version) 去重）
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.alert_events
(
    id String,
    cluster_id String DEFAULT 'default',
    rule_id String,
    rule_name String,
    service String,
    severity String,
    message String,
    value Float64,
    threshold Float64,
    timestamp DateTime64(3),
    count UInt32,
    first_timestamp DateTime64(3),
    last_timestamp DateTime64(3),
    status String,
    acknowledged_at DateTime64(3),
    acknowledged_by String,
    resolved_at DateTime64(3),
    resolved_by String,
    timeline String,
    investigation String,
    signature String,
    version UInt64,
    date Date
)
ENGINE = ReplacingMergeTree(version)
PARTITION BY date
ORDER BY (service, rule_id, id)
TTL toDateTime(last_timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- =============================================================================
-- k8s_events: K8s/IPMI 事件（从 event-collector 运行时 DDL 迁入；TTL 30 天）
-- =============================================================================
CREATE TABLE IF NOT EXISTS observability.k8s_events
(
    tenant_id String,
    cluster_id String DEFAULT 'default',
    ts DateTime64(9),
    namespace String,
    kind String,
    name String,
    reason String,
    type String,
    message String,
    involved_object String,
    source_component String,
    source String,
    node String DEFAULT '',
    time_bucket DateTime
)
ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, cluster_id, ts, involved_object, reason, name, message)
TTL time_bucket + INTERVAL 30 DAY;
