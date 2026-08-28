#!/usr/bin/env bash
set -euo pipefail

# Static contract gate: the 200k/1M performance command must remain a real
# fixture load with all required resource-evidence fields. This does not claim
# that HugeGraph or a browser was reachable; graph-load-test.sh reports that
# state at runtime.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="${repo_root}/deploy/scripts/graph-load-test.sh"
for required in \
  'vertices="${GRAPH_LOAD_VERTICES:-200000}"' \
  'edges="${GRAPH_LOAD_EDGES:-1000000}"' \
  '--load=true' \
  'fixture_loader' \
  'hugegraph_jvm_rss_heap' \
  'rocksdb_disk_wal' \
  'query_api_cpu_rss' \
  'orchestrator_cpu_rss' \
  'frontend_bundle_bytes' \
  'browser_long_tasks' \
  'GRAPH_LOAD_REQUIRE_RESOURCES'; do
  rg -n --fixed-strings -- "${required}" "${script}" >/dev/null || {
    echo "graph load contract failed: missing ${required}" >&2
    exit 1
  }
done
for required in 'GRAPH_API_TENANT_ID' 'GRAPH_API_CLUSTER_ID' 'scoped P95 queries'; do
  rg -n --fixed-strings -- "${required}" "${script}" >/dev/null || {
    echo "graph load contract failed: missing authorized scope requirement ${required}" >&2
    exit 1
  }
done
for required in 'vertex_types' 'service": 20_000' 'pod": 50_000' 'container": 50_000'; do
  rg -n --fixed-strings -- "${required}" "${script}" >/dev/null || {
    echo "graph load contract failed: missing ontology distribution check ${required}" >&2
    exit 1
  }
done

dry_run="${TMPDIR:-/tmp}/aiops-graph-load-contract.$$.json"
trap 'rm -f "${dry_run}"' EXIT
bash "${script}" --dry-run --output "${dry_run}" >/dev/null
python3 - "${dry_run}" <<'PY'
import json, sys
report = json.load(open(sys.argv[1], encoding="utf-8"))
assert report["vertices"] == 200_000
assert report["edges"] == 1_000_000
assert report["gate_status"] == "DRY_RUN"
assert "resource_gate" in report
PY
blocked="${TMPDIR:-/tmp}/aiops-graph-load-contract-blocked.$$.json"
trap 'rm -f "${dry_run}" "${blocked}"' EXIT
if HUGEGRAPH_URL=http://127.0.0.1:1 bash "${script}" --output "${blocked}" >/dev/null 2>&1; then
  echo "graph load contract failed: missing authorized scope was accepted" >&2
  exit 1
fi
python3 - "${blocked}" <<'PY'
import json, sys
report = json.load(open(sys.argv[1], encoding="utf-8"))
assert report["gate_status"] == "BLOCKED_BY_ENV"
assert "GRAPH_API_TENANT_ID" in report["reason"]
PY
echo "graph load contract tests passed"
