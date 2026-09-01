#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="${repo_root}/deploy/scripts/validate-observability-evidence.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-evidence-contract.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

python3 - "${tmp_dir}/complete.json" "${tmp_dir}/invalid.json" "${tmp_dir}/endpoint.json" <<'PY'
import json, sys
marker = "aiops-validation-fixture"
complete = {
    "marker": marker,
    "metrics": {"marker_found": True},
    "logs": {"marker_found": True},
    "events": {"marker_found": True},
    "deepflow": {"marker_found": True, "flow_count": 1, "span_count": 1},
    "dependency": {"marker_found": True, "edges": [{"relation_type": "DEPENDS_ON"}]},
    "rca": {
        "status": "confirmed",
        "time_range_start": "2026-08-28T00:00:00Z",
        "time_range_end": "2026-08-28T01:00:00Z",
        "symptom_time": "2026-08-28T00:30:00Z",
        "evidence": [{"category": "metrics"}, {"category": "logs"}],
        "final_graph_context": {"entity_uid": "service:orders"},
        "propagation_path": [{"entity_uid": "service:orders"}, {"entity_uid": "service:inventory"}],
        "subgraph_node_count": 10,
        "root_score": 0.91,
        "deterministic_root_score": 0.91,
        "relations": [{"relation_type": "CAUSED_BY", "confirmed": True}],
    },
}
invalid = dict(complete)
invalid["rca"] = dict(complete["rca"])
invalid["rca"]["evidence"] = [{"category": "metrics"}]
endpoint = dict(complete)
endpoint["rca"] = dict(complete["rca"])
endpoint["rca"]["symptom_time"] = endpoint["rca"]["time_range_end"]
json.dump(complete, open(sys.argv[1], "w", encoding="utf-8"))
json.dump(invalid, open(sys.argv[2], "w", encoding="utf-8"))
json.dump(endpoint, open(sys.argv[3], "w", encoding="utf-8"))
PY

if ! bash "${script}" --marker aiops-validation-fixture --fixture "${tmp_dir}/complete.json" --output "${tmp_dir}/complete-report.json" >/dev/null; then
  echo "observability evidence contract failed: complete fixture did not pass" >&2
  exit 1
fi
python3 - "${tmp_dir}/complete-report.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert data["gate_status"] == "PASS"
assert data["checks"]["rca"]["evidence_categories"] == 2
PY

if ! bash "${script}" --marker aiops-validation-fixture --fixture "${tmp_dir}/endpoint.json" --output "${tmp_dir}/endpoint-report.json" >/dev/null; then
  echo "observability evidence contract failed: default endpoint symptom_time was rejected" >&2
  exit 1
fi
python3 - "${tmp_dir}/endpoint-report.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert data["gate_status"] == "PASS"
PY

if bash "${script}" --marker aiops-validation-fixture --fixture "${tmp_dir}/invalid.json" --output "${tmp_dir}/invalid-report.json" >/dev/null 2>&1; then
  echo "observability evidence contract failed: incomplete RCA evidence was accepted" >&2
  exit 1
fi
python3 - "${tmp_dir}/invalid-report.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert data["gate_status"] == "FAIL"
assert any("evidence" in failure for failure in data["failures"])
PY
echo "observability evidence contract tests passed"
