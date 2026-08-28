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
echo "graph load contract tests passed"
