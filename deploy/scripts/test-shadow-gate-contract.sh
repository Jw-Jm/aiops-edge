#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="${repo_root}/deploy/scripts/shadow-gate.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-shadow-contract.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

python3 - "${tmp_dir}/complete.json" "${tmp_dir}/incomplete.json" <<'PY'
import json, sys
operations = {
    "entity": {"p95_ms": 99, "success_rate": 1},
    "alias_search": {"p95_ms": 199, "success_rate": 1},
    "one_hop": {"p95_ms": 199, "success_rate": 1},
    "two_hop": {"p95_ms": 499, "success_rate": 1},
    "shortest_path": {"p95_ms": 999, "success_rate": 1},
    "rca_candidate": {"p95_ms": 1499, "success_rate": 1},
    "impact": {"p95_ms": 1499, "success_rate": 1},
    "batch_mutation": {"p95_ms": 1999, "success_rate": 1},
}
complete = {
    "identity_mismatch": 0,
    "structural_mismatch": 0,
    "scope_leak": 0,
    "dead_outbox": 0,
    "outbox_oldest_p99_seconds": 29.9,
    "source_lag_seconds": {"kubernetes": 900, "kubevirt": 300, "hardware": 1800},
    "graph_api_5xx_rate_percent": 0.099,
    "graph_p95": operations,
    "trace_dependency_mismatch_percent": 0.999,
    "fixed_rca_scenario": "PASS",
}
json.dump(complete, open(sys.argv[1], "w", encoding="utf-8"))
json.dump({"identity_mismatch": 0}, open(sys.argv[2], "w", encoding="utf-8"))
PY

if bash "${script}" --report "${tmp_dir}/incomplete.json" --output "${tmp_dir}/incomplete-result.json" >/dev/null 2>&1; then
  echo "shadow gate contract failed: incomplete report was accepted" >&2
  exit 1
fi
if bash "${script}" --report "${tmp_dir}/missing.json" --output "${tmp_dir}/missing-result.json" >/dev/null 2>&1; then
  echo "shadow gate contract failed: missing report was accepted" >&2
  exit 1
fi
python3 - "${tmp_dir}/missing-result.json" <<'PY'
import json, sys
result = json.load(open(sys.argv[1], encoding="utf-8"))
assert result["gate"] == "BLOCKED_BY_ENV"
PY
bash "${script}" --report "${tmp_dir}/complete.json" --output "${tmp_dir}/complete-result.json" >/dev/null
python3 - "${tmp_dir}/complete-result.json" <<'PY'
import json, sys
result = json.load(open(sys.argv[1], encoding="utf-8"))
assert result["gate"] == "PASS"
assert not result["failures"]
assert result["thresholds"]["outbox_oldest_p99_seconds"] == 30
assert result["thresholds"]["source_lag_seconds"]["kubevirt"] == 300
PY

python3 - "${tmp_dir}/complete.json" "${tmp_dir}/trace-bad.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
data["trace_dependency_mismatch_percent"] = 1
json.dump(data, open(sys.argv[2], "w", encoding="utf-8"))
PY
if bash "${script}" --report "${tmp_dir}/trace-bad.json" --output "${tmp_dir}/trace-bad-result.json" >/dev/null 2>&1; then
  echo "shadow gate contract failed: trace mismatch boundary was accepted" >&2
  exit 1
fi
echo "shadow gate contract tests passed"
