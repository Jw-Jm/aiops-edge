#!/usr/bin/env bash
set -euo pipefail

# The DeepFlow ClickHouse is DeepFlow-owned. Ingest may receive DeepFlow data
# only through the official OTLP/gRPC exporter on port 4317.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

# Keep the forbidden vocabulary assembled here so this checker does not match
# its own source when it scans the runtime tree.
legacy_host="DEEPFLOW_CH_""HOST"
legacy_endpoints="DEEPFLOW_CH_""ENDPOINTS"
legacy_value="deepflow""ChHost"
legacy_port_value="deepflow""ChPort"
legacy_sync="DeepFlow""Syncer"
legacy_ctor="NewDeepFlow""Syncer"
legacy_retention="ensure""Retention"
legacy_query="query""DF"
legacy_exec="exec""DF"
legacy_endpoint="/v1/""deepflow"
legacy_service="deepflow""-clickhouse"

pattern="${legacy_host}|${legacy_endpoints}|${legacy_value}|${legacy_port_value}|syncInterval|spanSampleRate|ingestExternalEgress|${legacy_ctor}|${legacy_retention}|${legacy_query}|${legacy_exec}|${legacy_sync}|${legacy_endpoint}|${legacy_service}"
scan_paths=(ai-apm-ingest-go deploy/helm/aiops deploy/scripts)

if rg -n "$pattern" "${scan_paths[@]}" \
  --glob '*.go' --glob '*.yaml' --glob '*.sh' --glob '*.tpl' \
  --glob '!deploy/scripts/test-deepflow-runtime-boundary.sh'; then
  echo "legacy DeepFlow ClickHouse runtime references remain" >&2
  exit 1
fi

echo "DeepFlow runtime boundary scan: PASS"
