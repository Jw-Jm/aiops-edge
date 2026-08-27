#!/usr/bin/env bash
set -euo pipefail

# One-time convergence/rebuild for the derived Trace candidate index. The index
# is recreated with a newest-first physical key; trace_spans and
# trace_summary_state are never deleted or rewritten. Set
# REBUILD_LEGACY_INDEX=true explicitly before running.

ch_host="${CLICKHOUSE_HOST:-clickhouse.observability.svc.cluster.local}"
ch_port="${CLICKHOUSE_PORT:-9000}"
ch_user="${CLICKHOUSE_USER:-default}"
ch_password="${CLICKHOUSE_PASSWORD:-}"
lookback_days="${LOOKBACK_DAYS:-30}"
chunk_minutes="${CHUNK_MINUTES:-5}"

if [[ "${REBUILD_LEGACY_INDEX:-false}" != "true" ]]; then
  echo "refusing to rebuild derived Trace index without REBUILD_LEGACY_INDEX=true" >&2
  exit 2
fi
if ! [[ "${lookback_days}" =~ ^[0-9]+$ ]]; then
  echo "LOOKBACK_DAYS must be a non-negative integer" >&2
  exit 2
fi
if ! [[ "${chunk_minutes}" =~ ^[1-9][0-9]*$ ]] || (( chunk_minutes > 1440 )); then
  echo "CHUNK_MINUTES must be an integer between 1 and 1440" >&2
  exit 2
fi

ch() {
  if [[ -n "${ch_password}" ]]; then
    clickhouse-client --host "${ch_host}" --port "${ch_port}" --user "${ch_user}" --password "${ch_password}" "$@" </dev/null
  else
    clickhouse-client --host "${ch_host}" --port "${ch_port}" --user "${ch_user}" "$@" </dev/null
  fi
}

for attempt in $(seq 1 60); do
  if [[ "$(ch --query "SELECT count() FROM system.tables WHERE database='observability' AND name='trace_summary_index' FORMAT TSVRaw" 2>/dev/null)" == "1" ]]; then
    break
  fi
  sleep 5
done
if [[ "$(ch --query "SELECT count() FROM system.tables WHERE database='observability' AND name='trace_summary_index' FORMAT TSVRaw")" != "1" ]]; then
  echo "observability.trace_summary_index is missing" >&2
  exit 1
fi

legacy_v2="$(ch --query "SELECT count() FROM system.tables WHERE database='observability' AND name='trace_spans_to_summary_index_v2' FORMAT TSVRaw")"
if [[ "${legacy_v2}" == "1" ]]; then
  echo "dropping legacy duplicate Trace Summary index MV"
  ch --query "DROP VIEW IF EXISTS observability.trace_spans_to_summary_index_v2"
fi
ch --query "DROP TABLE IF EXISTS observability.trace_summary_index_v2_backfill_markers"

echo "rebuilding derived Trace Summary candidate index"
ch --query "DROP VIEW IF EXISTS observability.trace_spans_to_summary_index"
ch --query "DROP TABLE observability.trace_summary_index"
ch --query "TRUNCATE TABLE observability.trace_summary_index_backfill_markers"
ch --query "CREATE TABLE observability.trace_summary_index (tenant_id String, cluster_id String, date Date, time_bucket DateTime, trace_id String, latest_start DateTime64(9), latest_start_key Int64, service_name String DEFAULT '', search_text String DEFAULT '') ENGINE = MergeTree PARTITION BY date ORDER BY (tenant_id, date, latest_start_key, cluster_id, trace_id, service_name) TTL toDateTime(latest_start) + INTERVAL 30 DAY SETTINGS index_granularity=8192"
ch --query "CREATE MATERIALIZED VIEW observability.trace_spans_to_summary_index TO observability.trace_summary_index AS SELECT tenant_id, cluster_id, date, toStartOfFiveMinutes(start_time) AS time_bucket, trace_id, max(start_time) AS latest_start, -toUnixTimestamp64Nano(max(start_time)) AS latest_start_key, service_name, arrayStringConcat(groupUniqArray(concat(operation_name, ' ', http_url)), ' ') AS search_text FROM observability.trace_spans GROUP BY tenant_id, cluster_id, date, time_bucket, trace_id, service_name"

echo "backfilling the canonical Trace Summary candidate index"
LOOKBACK_DAYS="${lookback_days}" CHUNK_MINUTES="${chunk_minutes}" CLICKHOUSE_HOST="${ch_host}" CLICKHOUSE_PORT="${ch_port}" CLICKHOUSE_USER="${ch_user}" CLICKHOUSE_PASSWORD="${ch_password}" \
  "$(dirname "${BASH_SOURCE[0]}")/backfill-trace-summaries.sh"
echo "derived Trace Summary candidate index rebuilt"
