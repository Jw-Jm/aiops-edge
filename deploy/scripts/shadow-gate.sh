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
  mkdir -p "$(dirname "${output}")"
  python3 - "${report}" "${output}" <<'PY'
import json, sys
source, target = sys.argv[1:]
json.dump({"source": source, "gate": "BLOCKED_BY_ENV",
           "failures": ["shadow report is unavailable"]},
          open(target, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
  echo "BLOCKED_BY_ENV: shadow report not found at ${report}" >&2
  exit 2
fi
mkdir -p "$(dirname "${output}")"

python3 - "${report}" "${output}" <<'PY'
import json, math, sys

source, target = sys.argv[1:]
data = json.load(open(source, encoding="utf-8"))
failures = []

thresholds = {
    "identity_mismatch": 0,
    "structural_mismatch": 0,
    "scope_leak": 0,
    "dead_outbox": 0,
    "outbox_oldest_p99_seconds": 30,
    "source_lag_seconds": {"kubernetes": 900, "kubevirt": 300, "hardware": 1800},
    "graph_api_5xx_rate_percent": 0.1,
    "graph_p95": {
        "entity": 100, "alias_search": 200, "one_hop": 200, "two_hop": 500,
        "shortest_path": 1000, "rca_candidate": 1500, "impact": 1500,
        "batch_mutation": 2000,
    },
    "trace_dependency_mismatch_percent": 1,
    "fixed_rca_scenario": "PASS",
}

def finite_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)

def required(label, *paths):
    for path in paths:
        value = data
        found = True
        for part in path:
            if not isinstance(value, dict) or part not in value:
                found = False
                break
            value = value[part]
        if found:
            return value
    failures.append(label + ": missing")
    return None

def require_zero(label, *paths):
    value = required(label, *paths)
    if value is not None and (not finite_number(value) or value != 0):
        failures.append(label + ": expected 0")

require_zero("identity_mismatch", ("identity_mismatch",))
require_zero("structural_mismatch", ("structural_mismatch",))
require_zero("scope_leak", ("scope_leak",))
require_zero("dead_outbox", ("dead_outbox",), ("outbox", "dead"))

outbox_age = required("outbox_oldest_p99_seconds", ("outbox_oldest_p99_seconds",), ("outbox", "oldest_p99_seconds"))
if outbox_age is not None and (not finite_number(outbox_age) or outbox_age >= thresholds["outbox_oldest_p99_seconds"]):
    failures.append("outbox_oldest_p99_seconds: expected < 30")

source_lag = required("source_lag_seconds", ("source_lag_seconds",), ("sources",))
if not isinstance(source_lag, dict):
    if source_lag is not None:
        failures.append("source_lag_seconds: expected object")
else:
    for source_name, limit in thresholds["source_lag_seconds"].items():
        value = source_lag.get(source_name)
        if isinstance(value, dict):
            value = value.get("lag_seconds")
        if not finite_number(value):
            failures.append(f"source_lag_seconds.{source_name}: missing")
        elif value > limit:
            failures.append(f"source_lag_seconds.{source_name}: expected <= {limit}")

five_xx = required("graph_api_5xx_rate_percent", ("graph_api_5xx_rate_percent",), ("graph_api", "5xx_rate_percent"))
if five_xx is not None and (not finite_number(five_xx) or five_xx >= thresholds["graph_api_5xx_rate_percent"]):
    failures.append("graph_api_5xx_rate_percent: expected < 0.1")

graph_p95 = required("graph_p95", ("graph_p95",), ("graph", "p95"))
if not isinstance(graph_p95, dict):
    if graph_p95 is not None:
        failures.append("graph_p95: expected object")
else:
    expected_operations = set(thresholds["graph_p95"])
    if set(graph_p95) != expected_operations:
        missing = sorted(expected_operations - set(graph_p95))
        extra = sorted(set(graph_p95) - expected_operations)
        if missing:
            failures.append("graph_p95: missing " + ",".join(missing))
        if extra:
            failures.append("graph_p95: unexpected " + ",".join(extra))
    for operation, limit in thresholds["graph_p95"].items():
        item = graph_p95.get(operation)
        if not isinstance(item, dict):
            failures.append(f"graph_p95.{operation}: missing")
            continue
        p95 = item.get("p95_ms")
        success_rate = item.get("success_rate")
        if not finite_number(p95) or p95 >= limit:
            failures.append(f"graph_p95.{operation}: expected p95 < {limit}")
        if not finite_number(success_rate) or success_rate != 1:
            failures.append(f"graph_p95.{operation}: expected success_rate = 1")

trace_mismatch = required("trace_dependency_mismatch_percent", ("trace_dependency_mismatch_percent",), ("trace", "dependency_mismatch_percent"))
if trace_mismatch is not None and (not finite_number(trace_mismatch) or trace_mismatch >= thresholds["trace_dependency_mismatch_percent"]):
    failures.append("trace_dependency_mismatch_percent: expected < 1")

fixed_rca = required("fixed_rca_scenario", ("fixed_rca_scenario",), ("fixed_rca", "status"))
if fixed_rca != thresholds["fixed_rca_scenario"]:
    failures.append("fixed_rca_scenario: expected PASS")

result = {"source": source, "thresholds": thresholds, "input": data,
          "gate": "PASS" if not failures else "FAIL", "failures": failures}
json.dump(result, open(target, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps(result, ensure_ascii=False, indent=2))
if failures:
    raise SystemExit(1)
PY
