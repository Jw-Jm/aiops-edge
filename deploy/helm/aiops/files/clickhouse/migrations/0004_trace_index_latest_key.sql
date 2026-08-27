-- 0004-trace-index-latest-key
-- Rebuild only the derived Trace candidate index so the physical order is
-- newest-first within each tenant/date partition. Raw trace_spans and the
-- AggregatingMergeTree Trace Summary are never changed by this migration.

DROP VIEW IF EXISTS observability.trace_spans_to_summary_index;
DROP TABLE IF EXISTS observability.trace_summary_index;
DROP TABLE IF EXISTS observability.trace_summary_index_backfill_markers;

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

CREATE TABLE IF NOT EXISTS observability.trace_summary_index_backfill_markers
(
    `tenant_id` String DEFAULT '',
    `date` Date,
    `completed_at` DateTime64(3),
    `version` UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id, date);

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
