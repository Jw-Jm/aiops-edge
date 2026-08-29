#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="${repo_root}/deploy/scripts/graph-resource-snapshot.sh"

[[ -x "${script}" ]] || {
  echo "resource snapshot contract failed: collector is missing or not executable" >&2
  exit 1
}
for required in \
  'hugegraph_jvm_rss_heap' \
  'rocksdb_disk_wal' \
  'query_api_cpu_rss' \
  'ai_investigation_worker_cpu_rss' \
  'frontend_bundle_bytes' \
  'browser_long_tasks' \
  'not_collected' \
  '--fixture'; do
  rg -n --fixed-strings -- "${required}" "${script}" >/dev/null || {
    echo "resource snapshot contract failed: missing ${required}" >&2
    exit 1
  }
done

fixture="${TMPDIR:-/tmp}/aiops-resource-snapshot-contract.$$.json"
output="${TMPDIR:-/tmp}/aiops-resource-snapshot-contract-output.$$.json"
trap 'rm -f "${fixture}" "${output}"' EXIT
python3 - "${fixture}" <<'PY'
import json, sys
groups = {
    "hugegraph_jvm_rss_heap": {"status": "collected", "jvm_rss_bytes": 1, "heap_used_bytes": 1, "heap_max_bytes": 2},
    "rocksdb_disk_wal": {"status": "collected", "data_bytes": 1, "wal_bytes": 1},
    "query_api_cpu_rss": {"status": "collected", "cpu_millicores": 1, "rss_bytes": 1},
    "ai_investigation_worker_cpu_rss": {"status": "collected", "cpu_millicores": 1, "rss_bytes": 1},
    "frontend_bundle_bytes": {"status": "collected", "value": 1},
    "browser_long_tasks": {"status": "collected", "count": 0, "max_duration_ms": 0},
}
json.dump(groups, open(sys.argv[1], "w", encoding="utf-8"))
PY
bash "${script}" --fixture "${fixture}" --output "${output}" >/dev/null
python3 - "${output}" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert all(item.get("status") == "collected" for item in data.values())
PY
echo "graph resource snapshot contract tests passed"
