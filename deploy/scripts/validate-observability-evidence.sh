#!/usr/bin/env bash
set -euo pipefail

marker="${AIOPS_VALIDATION_DATA_MARKER:-}"
output="${AIOPS_EVIDENCE_REPORT_OUTPUT:-/tmp/aiops-real-evidence-report.json}"
fixture=""
namespace="${AIOPS_VALIDATION_NAMESPACE:-aiops-canary}"

usage() {
  cat <<'EOF'
Usage: validate-observability-evidence.sh --marker MARKER [--output PATH]
       [--fixture PATH]

Live mode reads explicitly configured evidence URLs:
  AIOPS_METRICS_EVIDENCE_URL, AIOPS_LOGS_EVIDENCE_URL,
  AIOPS_DEEPFLOW_EVIDENCE_URL, AIOPS_GRAPH_DEPENDENCY_EVIDENCE_URL,
  AIOPS_RCA_EVIDENCE_URL. Kubernetes events are read from the configured
namespace with kubectl. --fixture is only for deterministic contract tests.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --marker) marker="${2:?--marker requires a value}"; shift 2 ;;
    --output) output="${2:?--output requires a path}"; shift 2 ;;
    --fixture) fixture="${2:?--fixture requires a path}"; shift 2 ;;
    --namespace) namespace="${2:?--namespace requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
mkdir -p "$(dirname "${output}")"
if [[ -z "${marker}" ]]; then
  python3 - "${output}" <<'PY'
import json, sys
json.dump({"marker": "", "checks": {}, "failures": [],
           "blocked": ["validation marker is unavailable"],
           "gate_status": "BLOCKED_BY_ENV"},
          open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
  echo "BLOCKED_BY_ENV: AIOPS_VALIDATION_DATA_MARKER is required" >&2
  exit 2
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-evidence.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT
if [[ -n "${fixture}" ]]; then
  [[ -f "${fixture}" ]] || { echo "fixture not found: ${fixture}" >&2; exit 2; }
  cp "${fixture}" "${tmp_dir}/fixture.json"
else
  fetch_url() {
    local url="$1"
    if [[ -z "${url}" ]]; then
      return 0
    fi
    if [[ -n "${AIOPS_EVIDENCE_AUTH_HEADER:-}" ]]; then
      curl -fsS --max-time 30 -H "${AIOPS_EVIDENCE_AUTH_HEADER}" "${url}" 2>/dev/null || true
    else
      curl -fsS --max-time 30 "${url}" 2>/dev/null || true
    fi
  }
  fetch_url "${AIOPS_METRICS_EVIDENCE_URL:-}" >"${tmp_dir}/metrics.json"
  fetch_url "${AIOPS_LOGS_EVIDENCE_URL:-}" >"${tmp_dir}/logs.json"
  fetch_url "${AIOPS_DEEPFLOW_EVIDENCE_URL:-}" >"${tmp_dir}/deepflow.json"
  fetch_url "${AIOPS_GRAPH_DEPENDENCY_EVIDENCE_URL:-}" >"${tmp_dir}/dependency.json"
  fetch_url "${AIOPS_RCA_EVIDENCE_URL:-}" >"${tmp_dir}/rca.json"
  if command -v kubectl >/dev/null 2>&1; then
    kubectl -n "${namespace}" get events -o json 2>/dev/null >"${tmp_dir}/events.json" || :
  else
    : >"${tmp_dir}/events.json"
  fi
fi

if [[ -n "${fixture}" ]]; then
  python3 - "${marker}" "${output}" "${tmp_dir}/fixture.json" <<'PY'
import json, sys
marker, output, fixture = sys.argv[1:]
payload = json.load(open(fixture, encoding="utf-8"))
json.dump({"marker": marker, "payload": payload}, open(output, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
else
  python3 - "${marker}" "${output}" "${tmp_dir}" <<'PY'
import json, os, sys
marker, output, directory = sys.argv[1:]
payload = {}
for name in ("metrics", "logs", "events", "deepflow", "dependency", "rca"):
    path = os.path.join(directory, name + ".json")
    text = open(path, encoding="utf-8").read() if os.path.exists(path) else ""
    try:
        payload[name] = json.loads(text) if text.strip() else None
    except json.JSONDecodeError:
        payload[name] = text
json.dump({"marker": marker, "payload": payload}, open(output, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
fi

python3 - "${marker}" "${output}" <<'PY'
import datetime as dt
import json
import math
import sys

marker, output = sys.argv[1:]
document = json.load(open(output, encoding="utf-8"))
payload = document.get("payload", {})
failures = []
blocked = []

def as_text(value):
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    return json.dumps(value, ensure_ascii=False)

def marker_found(value):
    if isinstance(value, dict) and value.get("marker_found") is True:
        return True
    return marker in as_text(value)

def external_check(key, label):
    value = payload.get(key)
    if value is None or value == "":
        blocked.append(label + ": unavailable")
        return {"status": "BLOCKED_BY_ENV", "reason": "evidence source unavailable"}
    if not marker_found(value):
        failures.append(label + ": marker not found")
        return {"status": "FAIL", "reason": "marker not found"}
    return {"status": "PASS", "marker_found": True}

checks = {
    "metrics": external_check("metrics", "metrics"),
    "logs": external_check("logs", "logs"),
    "kubernetes_events": external_check("events", "kubernetes_events"),
}

deepflow_value = payload.get("deepflow")
if deepflow_value is None or deepflow_value == "":
    blocked.append("deepflow: unavailable")
    checks["deepflow"] = {"status": "BLOCKED_BY_ENV", "reason": "evidence source unavailable"}
elif not marker_found(deepflow_value):
    failures.append("deepflow: marker not found")
    checks["deepflow"] = {"status": "FAIL", "reason": "marker not found"}
else:
    flow_count = deepflow_value.get("flow_count", 0) if isinstance(deepflow_value, dict) else 0
    span_count = deepflow_value.get("span_count", 0) if isinstance(deepflow_value, dict) else 0
    deepflow_failures = []
    if not isinstance(flow_count, (int, float)) or flow_count < 1:
        deepflow_failures.append("flow")
        failures.append("deepflow: no flow evidence")
    if not isinstance(span_count, (int, float)) or span_count < 1:
        deepflow_failures.append("span")
        failures.append("deepflow: no span evidence")
    checks["deepflow"] = {"status": "PASS" if not deepflow_failures else "FAIL",
                           "flow_count": flow_count, "span_count": span_count}

dependency = payload.get("dependency")
if dependency is None or dependency == "":
    blocked.append("service_dependency: unavailable")
    checks["service_dependency"] = {"status": "BLOCKED_BY_ENV", "reason": "evidence source unavailable"}
elif not marker_found(dependency):
    failures.append("service_dependency: marker not found")
    checks["service_dependency"] = {"status": "FAIL", "reason": "marker not found"}
else:
    edges = dependency.get("edges", []) if isinstance(dependency, dict) else []
    depends_on = [edge for edge in edges if edge.get("relation_type") == "DEPENDS_ON"] if isinstance(edges, list) else []
    if not depends_on:
        failures.append("service_dependency: DEPENDS_ON edge missing")
    checks["service_dependency"] = {"status": "PASS" if depends_on else "FAIL", "depends_on_edges": len(depends_on)}

rca_payload = payload.get("rca")
if rca_payload is None or rca_payload == "":
    blocked.append("rca: unavailable")
    checks["rca"] = {"status": "BLOCKED_BY_ENV", "reason": "RCA run unavailable"}
else:
    rca = rca_payload.get("rca", rca_payload) if isinstance(rca_payload, dict) else {}
    rca_failures = []
    try:
        start = dt.datetime.fromisoformat(str(rca["time_range_start"]).replace("Z", "+00:00"))
        end = dt.datetime.fromisoformat(str(rca["time_range_end"]).replace("Z", "+00:00"))
        symptom = dt.datetime.fromisoformat(str(rca["symptom_time"]).replace("Z", "+00:00"))
        # Canonical Run dispatch deterministically defaults symptom_time to
        # the persisted window_end when no separate symptom timestamp exists.
        # The frozen window is therefore closed at both ends; rejecting the
        # endpoint here would make every valid default-symptom Run unverifiable.
        if not start <= symptom <= end:
            rca_failures.append("time window does not contain symptom_time")
    except (KeyError, TypeError, ValueError):
        rca_failures.append("frozen time window is incomplete")
    evidence = rca.get("evidence", [])
    categories = {item.get("category") for item in evidence if isinstance(item, dict) and item.get("category")}
    if len(categories) < 2:
        rca_failures.append("evidence requires at least two independent categories")
    if not isinstance(rca.get("final_graph_context"), dict) or not rca["final_graph_context"]:
        rca_failures.append("final graph context is missing")
    propagation = rca.get("propagation_path", [])
    subgraph_count = rca.get("subgraph_node_count")
    if not isinstance(propagation, list) or not propagation:
        rca_failures.append("propagation path is missing")
    elif not isinstance(subgraph_count, (int, float)) or len(propagation) >= subgraph_count:
        rca_failures.append("propagation path is not a bounded path")
    root_score = rca.get("root_score")
    deterministic_score = rca.get("deterministic_root_score")
    if not isinstance(root_score, (int, float)) or not isinstance(deterministic_score, (int, float)) or not math.isclose(root_score, deterministic_score, rel_tol=0, abs_tol=1e-9):
        rca_failures.append("root score differs from deterministic backend score")
    caused_by = [item for item in rca.get("relations", []) if item.get("relation_type") == "CAUSED_BY"]
    if caused_by and (rca.get("status") != "confirmed" or any(item.get("confirmed") is not True for item in caused_by)):
        rca_failures.append("CAUSED_BY is only allowed for confirmed RCA")
    if rca_failures:
        failures.extend("rca: " + item for item in rca_failures)
    checks["rca"] = {"status": "PASS" if not rca_failures else "FAIL",
                      "evidence_categories": len(categories),
                      "propagation_path_length": len(propagation) if isinstance(propagation, list) else 0}

if failures:
    gate_status = "FAIL"
elif blocked:
    gate_status = "BLOCKED_BY_ENV"
else:
    gate_status = "PASS"
result = {"marker": marker, "checks": checks, "failures": failures,
          "blocked": blocked, "gate_status": gate_status}
json.dump(result, open(output, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps(result, ensure_ascii=False, indent=2))
if gate_status == "FAIL":
    raise SystemExit(1)
if gate_status == "BLOCKED_BY_ENV":
    raise SystemExit(2)
PY
