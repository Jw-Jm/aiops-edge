#!/usr/bin/env bash
set -euo pipefail

report="${SHADOW_REPORT:-/tmp/aiops-shadow-report.json}"
output="${SHADOW_GATE_OUTPUT:-/tmp/aiops-shadow-gate-result.json}"
usage() { echo "Usage: shadow-gate.sh [--report PATH] [--output PATH]"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --report) report="${2:?--report requires a path}"; shift 2 ;;
    --output) output="${2:?--output requires a path}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
if [[ ! -f "${report}" ]]; then
  echo "BLOCKED_BY_ENV: shadow report not found at ${report}" >&2
  exit 2
fi
python3 - "${report}" "${output}" <<'PY'
import json, os, sys
source, target = sys.argv[1:]
data = json.load(open(source, encoding="utf-8"))
thresholds = {
    "structural_mismatch": int(os.environ.get("SHADOW_MAX_STRUCTURAL", "0")),
    "identity_mismatch": int(os.environ.get("SHADOW_MAX_IDENTITY", "0")),
    "scope_leak": int(os.environ.get("SHADOW_MAX_SCOPE_LEAK", "0")),
    "lag_seconds": float(os.environ.get("SHADOW_MAX_LAG_SECONDS", "120")),
    "p95_ms": float(os.environ.get("SHADOW_MAX_P95_MS", "500")),
}
failures = []
for key in ("structural_mismatch", "identity_mismatch", "scope_leak"):
    if float(data.get(key, 0)) > thresholds[key]: failures.append(key)
for key in ("lag_seconds", "p95_ms"):
    if float(data.get(key, 0)) > thresholds[key]: failures.append(key)
result = {"source": source, "thresholds": thresholds, "input": data,
          "gate": "PASS" if not failures else "FAIL", "failures": failures}
json.dump(result, open(target, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps(result, ensure_ascii=False, indent=2))
if failures: raise SystemExit(1)
PY
