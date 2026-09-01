#!/usr/bin/env bash
set -euo pipefail

# Backfill the Trace Summary/Index in bounded date partitions. Run this script
# in the Platform ClickHouse container (or any image with clickhouse-client).
# It never deletes or rewrites trace_spans. Summary state writes are mergeable,
# while index writes are guarded by per-partition markers; rerunning a completed
# partition is skipped and an interrupted partition must be reconciled before
# retrying if duplicate index rows are not acceptable.

ch_host="${CLICKHOUSE_HOST:-clickhouse.observability.svc.cluster.local}"
ch_port="${CLICKHOUSE_PORT:-9000}"
ch_user="${CLICKHOUSE_USER:-default}"
ch_password="${CLICKHOUSE_PASSWORD:-}"
lookback_days="${LOOKBACK_DAYS:-30}"
tenant_id="${TENANT_ID:-}"
dry_run="${DRY_RUN:-false}"
chunk_minutes="${CHUNK_MINUTES:-5}"

if ! [[ "${lookback_days}" =~ ^[0-9]+$ ]]; then
  echo "LOOKBACK_DAYS must be a non-negative integer" >&2
  exit 2
fi
if [[ "${tenant_id}" =~ [^a-zA-Z0-9._:-] ]]; then
  echo "TENANT_ID contains unsupported characters" >&2
  exit 2
fi
if [[ "${dry_run}" != "true" && "${dry_run}" != "false" ]]; then
  echo "DRY_RUN must be true or false" >&2
  exit 2
fi
if ! [[ "${chunk_minutes}" =~ ^[1-9][0-9]*$ ]] || (( chunk_minutes > 1440 )); then
  echo "CHUNK_MINUTES must be an integer between 1 and 1440" >&2
  exit 2
fi

ch() {
  if [[ -n "${ch_password}" ]]; then
    clickhouse-client --host "${ch_host}" --port "${ch_port}" --user "${ch_user}" --password="${ch_password}" "$@" </dev/null
  else
    clickhouse-client --host "${ch_host}" --port "${ch_port}" --user "${ch_user}" "$@" </dev/null
  fi
}

summary_exists=""
for attempt in $(seq 1 60); do
  if summary_exists="$(ch --query "SELECT count() FROM system.tables WHERE database='observability' AND name='trace_summary_state' FORMAT TSVRaw" 2>/dev/null)" && [[ "${summary_exists}" == "1" ]]; then
    break
  fi
  sleep 5
done
if [[ "${summary_exists}" != "1" ]]; then
  echo "observability.trace_summary_state is missing; apply ClickHouse bootstrap/migration first" >&2
  exit 1
fi

tenant_clause=""
if [[ -n "${tenant_id}" ]]; then
  tenant_clause=" AND tenant_id='${tenant_id}'"
fi

dates="$(ch --query "SELECT toString(date) FROM observability.trace_spans WHERE date >= today() - INTERVAL ${lookback_days} DAY${tenant_clause} GROUP BY date ORDER BY date FORMAT TSVRaw")"
if [[ -z "${dates}" ]]; then
  echo "trace summary backfill: no source partitions in lookback window"
  exit 0
fi

while IFS= read -r date_value; do
  [[ -z "${date_value}" ]] && continue
  marker_clause="date=toDate('${date_value}')"
  summary_done="0"
  index_done="0"
  if [[ -z "${tenant_id}" ]]; then
    summary_done="$(ch --query "SELECT count() FROM observability.trace_summary_backfill_markers WHERE ${marker_clause} AND tenant_id='' FORMAT TSVRaw")"
    index_done="$(ch --query "SELECT count() FROM observability.trace_summary_index_backfill_markers WHERE ${marker_clause} AND tenant_id='' FORMAT TSVRaw")"
  fi
  if [[ "${summary_done}" == "1" && "${index_done}" == "1" ]]; then
    echo "trace summary backfill: skip ${date_value} (already marked)"
    continue
  fi

  echo "trace summary backfill: processing ${date_value}"
  if [[ "${dry_run}" == "true" ]]; then
    continue
  fi

  chunk_start=0
  while (( chunk_start < 1440 )); do
    chunk_end=$((chunk_start + chunk_minutes))
    if (( chunk_end > 1440 )); then
      chunk_end=1440
    fi
    if [[ "${summary_done}" != "1" ]]; then
      ch --query "
        INSERT INTO observability.trace_summary_state
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
        WHERE date=toDate('${date_value}')${tenant_clause}
          AND start_time >= toDateTime('${date_value} 00:00:00') + INTERVAL ${chunk_start} MINUTE
          AND start_time < toDateTime('${date_value} 00:00:00') + INTERVAL ${chunk_end} MINUTE
        GROUP BY tenant_id, cluster_id, date, trace_id
        SETTINGS max_threads=1,
          max_bytes_before_external_group_by=33554432,
          max_bytes_before_external_sort=33554432"
    fi
    if [[ "${index_done}" != "1" ]]; then
      ch --query "
        INSERT INTO observability.trace_summary_index
          (tenant_id, cluster_id, date, time_bucket, trace_id, latest_start, latest_start_key, service_name, search_text)
        SELECT tenant_id, cluster_id, date, toStartOfFiveMinutes(start_time) AS time_bucket,
          trace_id, max(start_time) AS latest_start, -toUnixTimestamp64Nano(max(start_time)) AS latest_start_key, service_name,
          arrayStringConcat(groupUniqArray(concat(operation_name, ' ', http_url)), ' ') AS search_text
        FROM observability.trace_spans
        WHERE date=toDate('${date_value}')${tenant_clause}
          AND start_time >= toDateTime('${date_value} 00:00:00') + INTERVAL ${chunk_start} MINUTE
          AND start_time < toDateTime('${date_value} 00:00:00') + INTERVAL ${chunk_end} MINUTE
        GROUP BY tenant_id, cluster_id, date, time_bucket, trace_id, service_name
        SETTINGS max_threads=1,
          max_bytes_before_external_group_by=33554432,
          max_bytes_before_external_sort=33554432"
    fi
    chunk_start=$chunk_end
  done

  marker_tenant="${tenant_id}"
  if [[ "${summary_done}" != "1" ]]; then
    ch --query "INSERT INTO observability.trace_summary_backfill_markers (tenant_id, date, completed_at, version) VALUES ('${marker_tenant}', toDate('${date_value}'), now64(3), toUnixTimestamp64Milli(now64(3)))"
  fi
  if [[ "${index_done}" != "1" ]]; then
    ch --query "INSERT INTO observability.trace_summary_index_backfill_markers (tenant_id, date, completed_at, version) VALUES ('${marker_tenant}', toDate('${date_value}'), now64(3), toUnixTimestamp64Milli(now64(3)))"
  fi
done <<< "${dates}"

echo "trace summary backfill: completed"
