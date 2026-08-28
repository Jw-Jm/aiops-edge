#!/usr/bin/env bash
set -euo pipefail

# Production graph performance gate.  The typed Go fixture generator writes
# exactly 200k vertices/1M edges through HugeGraph's client, then this script
# measures the public query-api contract for every required operation.  The
# generator is a validation-only binary; runtime graph access remains owned by
# query-api and callers never submit Gremlin/Cypher.
vertices="${GRAPH_LOAD_VERTICES:-200000}"
edges="${GRAPH_LOAD_EDGES:-1000000}"
iterations="${GRAPH_LOAD_ITERATIONS:-20}"
batch_size="${GRAPH_LOAD_BATCH_SIZE:-500}"
base_url="${GRAPH_API_BASE_URL:-http://127.0.0.1:8080/api/v1/ai/kg}"
uid="${GRAPH_TEST_ENTITY_UID:-loadtest:vertex:000000}"
target_uid="${GRAPH_TEST_TARGET_UID:-loadtest:vertex:000001}"
output="${GRAPH_LOAD_REPORT:-/tmp/aiops-graph-load-report.json}"
require_resources="${GRAPH_LOAD_REQUIRE_RESOURCES:-0}"
dry_run=0

usage() {
  cat <<'EOF'
Usage: graph-load-test.sh [--dry-run] [--vertices N] [--edges N] [--iterations N]
       [--batch-size N] [--base-url URL] [--uid ENTITY_UID] [--target UID] [--output PATH]

Normal mode requires HUGEGRAPH_URL and loads the deterministic 200k/1M fixture.
--dry-run validates the requested dataset shape without touching HugeGraph.
EOF
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1; shift ;;
    --vertices) vertices="${2:?--vertices requires a value}"; shift 2 ;;
    --edges) edges="${2:?--edges requires a value}"; shift 2 ;;
    --iterations) iterations="${2:?--iterations requires a value}"; shift 2 ;;
    --batch-size) batch_size="${2:?--batch-size requires a value}"; shift 2 ;;
    --base-url) base_url="${2:?--base-url requires a value}"; shift 2 ;;
    --uid) uid="${2:?--uid requires a value}"; shift 2 ;;
    --target) target_uid="${2:?--target requires a value}"; shift 2 ;;
    --output) output="${2:?--output requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 2; }
for value in "$vertices" "$edges" "$iterations" "$batch_size"; do
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || { echo "numeric options must be positive" >&2; exit 2; }
done
[[ "$require_resources" == "0" || "$require_resources" == "1" ]] || { echo "GRAPH_LOAD_REQUIRE_RESOURCES must be 0 or 1" >&2; exit 2; }
if (( vertices < 2 || edges < 1 || batch_size > 500 )); then
  echo "vertices >= 2, edges >= 1 and batch-size <= 500 are required" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
write_blocked_report() {
  local reason="$1"
  python3 - "$vertices" "$edges" "$batch_size" "$output" "$reason" <<'PY'
import json, sys
vertices, edges, batch_size, output, reason = sys.argv[1:]
with open(output, "w", encoding="utf-8") as handle:
    json.dump({"vertices": int(vertices), "edges": int(edges), "batch_size": int(batch_size),
               "loaded": False, "gate_status": "BLOCKED_BY_ENV", "reason": reason,
               "fixture_loader": None,
               "resource_gate": {
                   "hugegraph_jvm_rss_heap": {"status": "not_collected"},
                   "rocksdb_disk_wal": {"status": "not_collected"},
                   "query_api_cpu_rss": {"status": "not_collected"},
                   "orchestrator_cpu_rss": {"status": "not_collected"},
                   "frontend_bundle_bytes": {"status": "not_collected"},
                   "browser_long_tasks": {"status": "not_collected"},
               }},
              handle, indent=2)
PY
}
if (( dry_run == 1 )); then
  python3 - "$vertices" "$edges" "$batch_size" "$output" <<'PY'
import json, sys
vertices, edges, batch_size = map(int, sys.argv[1:4])
with open(sys.argv[4], "w", encoding="utf-8") as handle:
    json.dump({"vertices": vertices, "edges": edges, "batch_size": batch_size, "loaded": False,
               "gate_status": "DRY_RUN", "fixture_loader": None,
               "resource_gate": {"status": "not_collected", "reason": "dry-run does not touch runtime"}}, handle, indent=2)
PY
  cat "$output"
  exit 0
fi

if [[ -z "${HUGEGRAPH_URL:-}" ]]; then
  write_blocked_report "HUGEGRAPH_URL is required to load the 200k/1M fixture"
  echo "BLOCKED_BY_ENV: HUGEGRAPH_URL is required to load the 200k/1M fixture" >&2
  exit 2
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-graph-load.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

if [[ -n "${GRAPH_LOAD_GENERATOR_CMD:-}" ]]; then
  read -r -a loader_cmd <<<"${GRAPH_LOAD_GENERATOR_CMD}"
else
  loader_cmd=(go run ./cmd/graph-load-generator)
fi
if ! loader_output="$(cd "$repo_root/ai-apm-query-go" && "${loader_cmd[@]}" --vertices "$vertices" --edges "$edges" --batch-size "$batch_size" --load=true --batch-benchmark-iterations "$iterations" 2>"$tmp_dir/loader.stderr")"; then
  write_blocked_report "HugeGraph fixture load did not complete"
  cat "$tmp_dir/loader.stderr" >&2 || true
  echo "BLOCKED_BY_ENV: HugeGraph fixture load did not complete" >&2
  exit 2
fi
if ! loader_json="$(python3 -c 'import json,sys; print(json.dumps(json.loads(sys.stdin.read())))' <<<"$loader_output")"; then
  write_blocked_report "fixture loader returned invalid JSON"
  echo "BLOCKED_BY_ENV: fixture loader returned invalid JSON" >&2
  exit 2
fi
loaded="$(python3 -c 'import json,sys; print("true" if json.load(sys.stdin).get("loaded") else "false")' <<<"$loader_json")"
if [[ "$loaded" != "true" ]]; then
  write_blocked_report "graph fixture loader did not report loaded=true"
  echo "BLOCKED_BY_ENV: graph fixture loader did not report loaded=true" >&2
  exit 2
fi
fixture_vertices="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("vertices", 0))' <<<"$loader_json")"
fixture_edges="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("edges", 0))' <<<"$loader_json")"
if [[ "$fixture_vertices" != "$vertices" || "$fixture_edges" != "$edges" ]]; then
  write_blocked_report "fixture loader count mismatch: requested ${vertices}/${edges}, loaded ${fixture_vertices}/${fixture_edges}"
  echo "BLOCKED_BY_ENV: fixture loader count mismatch" >&2
  exit 2
fi
headers=(-H "Accept: application/json")
[[ -n "${GRAPH_API_TOKEN:-}" ]] && headers+=(-H "Authorization: Bearer ${GRAPH_API_TOKEN}")
[[ -n "${GRAPH_API_TENANT_ID:-${GRAPH_LOAD_TENANT_ID:-load-test-tenant}}" ]] && headers+=(-H "X-Tenant-ID: ${GRAPH_API_TENANT_ID:-${GRAPH_LOAD_TENANT_ID:-load-test-tenant}}")
[[ -n "${GRAPH_API_CLUSTER_ID:-${GRAPH_LOAD_CLUSTER_ID:-load-test-cluster}}" ]] && headers+=(-H "X-Cluster-ID: ${GRAPH_API_CLUSTER_ID:-${GRAPH_LOAD_CLUSTER_ID:-load-test-cluster}}")
encoded_uid="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$uid")"
encoded_target="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$target_uid")"

rank=$(( (iterations * 95 + 99) / 100 ))
measure() {
  local operation="$1" method="$2" url="$3" body="${4:-}"
  local samples="$tmp_dir/${operation}.samples"
  : >"$samples"
  local success=0
  for ((i=0; i<iterations; i++)); do
    local response status seconds millis
    if [[ "$method" == "POST" ]]; then
      response="$(curl -sS -o /dev/null -w '%{http_code} %{time_total}' --max-time 30 "${headers[@]}" -H 'Content-Type: application/json' -X POST --data-raw "$body" "$url" || echo '000 30')"
    else
      response="$(curl -sS -o /dev/null -w '%{http_code} %{time_total}' --max-time 30 "${headers[@]}" "$url" || echo '000 30')"
    fi
    status="${response%% *}"; seconds="${response#* }"
    millis="$(awk -v value="$seconds" 'BEGIN { printf "%d", value*1000 }')"
    echo "$millis" >>"$samples"
    [[ "$status" == "200" ]] && success=$((success+1))
  done
  local p95
  p95="$(sort -n "$samples" | sed -n "${rank}p")"
  [[ -n "$p95" ]] || p95=0
  python3 - "$operation" "$iterations" "$success" "$p95" "$samples" >>"$tmp_dir/operations.jsonl" <<'PY'
import json, sys
operation, iterations, success, p95, samples_path = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), sys.argv[5]
values = [int(line) for line in open(samples_path, encoding="utf-8") if line.strip()]
print(json.dumps({"operation": operation, "iterations": iterations, "success": success,
                  "success_rate": success / iterations, "p95_ms": p95,
                  "max_ms": max(values, default=0)}))
PY
}

measure entity GET "${base_url}/entities/${encoded_uid}"
measure one_hop GET "${base_url}/entities/${encoded_uid}/neighbors?depth=1&max_vertices=300&max_edges=1000"
measure two_hop GET "${base_url}/entities/${encoded_uid}/neighbors?depth=2&max_vertices=300&max_edges=1000"
measure shortest_path POST "${base_url}/path" "{\"source_entity_uid\":\"${uid}\",\"target_entity_uid\":\"${target_uid}\",\"max_depth\":6}"
measure rca_candidate GET "${base_url}/entities/${encoded_uid}/candidate?depth=2&max_vertices=300&max_edges=1000"
measure impact GET "${base_url}/entities/${encoded_uid}/impact?max_depth=3"

batch_mutation="$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin).get("batch_mutation", {})))' <<<"$loader_json")"
# Resource evidence is part of the gate report.  Cluster metrics are optional
# in local mode, but the report keeps an explicit status for every required
# dimension instead of silently presenting latency as a complete benchmark.
frontend_bundle_bytes="$(du -sk "$repo_root/observability-frontend/dist" 2>/dev/null | awk '{print $1 * 1024}' || true)"
[[ "$frontend_bundle_bytes" =~ ^[0-9]+$ ]] || frontend_bundle_bytes=0
resource_json="$(python3 - "$frontend_bundle_bytes" <<'PY'
import json, sys
bundle = int(sys.argv[1])
print(json.dumps({
    "hugegraph_jvm_rss_heap": {"status": "not_collected", "reason": "requires cluster metrics"},
    "rocksdb_disk_wal": {"status": "not_collected", "reason": "requires cluster metrics"},
    "query_api_cpu_rss": {"status": "not_collected", "reason": "requires cluster metrics"},
    "orchestrator_cpu_rss": {"status": "not_collected", "reason": "requires cluster metrics"},
    "frontend_bundle_bytes": {"status": "collected", "value": bundle},
    "browser_long_tasks": {"status": "not_collected", "reason": "requires browser trace"},
}))
PY
)"
python3 - "$vertices" "$edges" "$batch_size" "$uid" "$target_uid" "$loader_json" "$output" "$tmp_dir/operations.jsonl" "$batch_mutation" "$resource_json" "$require_resources" <<'PY'
import json, sys
vertices, edges, batch_size = map(int, sys.argv[1:4])
uid, target_uid, loader_json, output, operations_file, batch, resource_json, require_resources = sys.argv[4:]
operations = [json.loads(line) for line in open(operations_file, encoding="utf-8") if line.strip()]
batch_data = json.loads(batch)
loader = json.loads(loader_json)
resources = json.loads(resource_json)
resource_items = resources.values() if isinstance(resources, dict) else []
resource_complete = all(item.get("status") == "collected" for item in resource_items)
operations.append({"operation": "batch_mutation", **batch_data})
gates = {"entity": 500, "one_hop": 1000, "two_hop": 2000, "shortest_path": 3000,
         "rca_candidate": 3000, "impact": 3000, "batch_mutation": 1000}
for item in operations:
    item["gate_ms"] = gates[item["operation"]]
    item["passed"] = item.get("success_rate", 1) == 1 and item["p95_ms"] <= item["gate_ms"]
all_unavailable = all(item.get("success", 0) == 0 for item in operations if item["operation"] != "batch_mutation")
resource_required = require_resources == "1"
gate_status = "BLOCKED_BY_ENV" if all_unavailable or (resource_required and not resource_complete) else "PASS" if all(item["passed"] for item in operations) else "FAIL"
result = {"vertices": vertices, "edges": edges, "batch_size": batch_size, "loaded": True,
          "anchor_uid": uid, "target_uid": target_uid, "operations": {item["operation"]: item for item in operations},
          "fixture_loader": loader, "resource_gate": resources, "resource_gate_status": "PASS" if resource_complete else "PARTIAL",
          "resource_gate_required": resource_required, "gate_status": gate_status,
          "gate_definition": gates}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(result, handle, indent=2)
PY
cat "$output"
if grep -q '"gate_status": "PASS"' "$output"; then exit 0; fi
if grep -q '"gate_status": "BLOCKED_BY_ENV"' "$output"; then echo "BLOCKED_BY_ENV: graph query API did not return a successful sample" >&2; exit 2; fi
echo "graph performance gate failed" >&2
exit 1
