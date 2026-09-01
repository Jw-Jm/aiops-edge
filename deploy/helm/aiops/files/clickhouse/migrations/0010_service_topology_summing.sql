-- 0010-service-topology-summing
-- service_topology receives incremental flushes from unified ingest.  The old
-- ReplacingMergeTree key is minute-granular, so repeated flushes for the same
-- minute can discard valid call/error counts.  Replace it with a SummingMergeTree
-- while preserving all existing rows and the public table name.

CREATE TABLE IF NOT EXISTS observability.service_topology_summing
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
ENGINE = SummingMergeTree
PARTITION BY date
ORDER BY (tenant_id, cluster_id, source_service, target_service, date, time_bucket)
TTL toDateTime(time_bucket) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

TRUNCATE TABLE observability.service_topology_summing;

INSERT INTO observability.service_topology_summing
    (tenant_id, cluster_id, source_service, target_service, time_bucket, call_count, error_count, avg_duration_ns, date)
SELECT tenant_id, cluster_id, source_service, target_service, time_bucket, call_count, error_count, avg_duration_ns, date
FROM observability.service_topology;

RENAME TABLE observability.service_topology TO observability.service_topology_replacing_legacy,
             observability.service_topology_summing TO observability.service_topology;
