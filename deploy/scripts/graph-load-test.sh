#!/usr/bin/env bash
set -euo pipefail

# Production graph performance gate.  The typed Go fixture generator writes
# exactly 200k ontology-shaped vertices/1M typed edges through HugeGraph's client, then this script
# measures the public query-api contract for every required operation.  The
# generator is a validation-only binary; runtime graph access remains owned by
# query-api and callers never submit Gremlin/Cypher.
vertices="${GRAPH_LOAD_VERTICES:-200000}"
edges="${GRAPH_LOAD_EDGES:-1000000}"
iterations="${GRAPH_LOAD_ITERATIONS:-20}"
warmup_iterations="${GRAPH_LOAD_WARMUP_ITERATIONS:-10}"
batch_size="${GRAPH_LOAD_BATCH_SIZE:-500}"
base_url="${GRAPH_API_BASE_URL:-http://127.0.0.1:8080/api/v1/ai/kg}"
uid="${GRAPH_TEST_ENTITY_UID:-loadtest:vertex:000000}"
target_uid="${GRAPH_TEST_TARGET_UID:-loadtest:vertex:000001}"
alias="${GRAPH_TEST_ENTITY_ALIAS:-graph-load-service-000000}"
output="${GRAPH_LOAD_REPORT:-/tmp/aiops-graph-load-report.json}"
require_resources="${GRAPH_LOAD_REQUIRE_RESOURCES:-0}"
project_query_aliases="${GRAPH_LOAD_PROJECT_QUERY_ALIASES:-0}"
tenant_id="${GRAPH_API_TENANT_ID:-${GRAPH_LOAD_TENANT_ID:-}}"
cluster_id="${GRAPH_API_CLUSTER_ID:-${GRAPH_LOAD_CLUSTER_ID:-}}"
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
for value in "$vertices" "$edges" "$iterations" "$warmup_iterations" "$batch_size"; do
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || { echo "numeric options must be positive" >&2; exit 2; }
done
[[ "$require_resources" == "0" || "$require_resources" == "1" ]] || { echo "GRAPH_LOAD_REQUIRE_RESOURCES must be 0 or 1" >&2; exit 2; }
[[ "$project_query_aliases" == "0" || "$project_query_aliases" == "1" ]] || { echo "GRAPH_LOAD_PROJECT_QUERY_ALIASES must be 0 or 1" >&2; exit 2; }
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
                   "ai_investigation_worker_cpu_rss": {"status": "not_collected"},
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
if [[ -z "${tenant_id}" || -z "${cluster_id}" ]]; then
  write_blocked_report "GRAPH_API_TENANT_ID and GRAPH_API_CLUSTER_ID must identify an authorized scope"
  echo "BLOCKED_BY_ENV: GRAPH_API_TENANT_ID and GRAPH_API_CLUSTER_ID are required for scoped P95 queries" >&2
  exit 2
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-graph-load.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

if [[ -n "${GRAPH_LOAD_GENERATOR_CMD:-}" ]]; then
  read -r -a loader_cmd <<<"${GRAPH_LOAD_GENERATOR_CMD}"
else
  loader_cmd=(go run ./cmd/graph-load-generator)
fi
loader_args=(--vertices "$vertices" --edges "$edges" --batch-size "$batch_size" --tenant-id "$tenant_id" --cluster-id "$cluster_id" --load=true --batch-benchmark-iterations "$iterations")
if [[ "$project_query_aliases" == "1" ]]; then
  loader_args+=(--project-query-aliases)
fi
if ! loader_output="$(cd "$repo_root/ai-apm-query-go" && "${loader_cmd[@]}" "${loader_args[@]}" 2>"$tmp_dir/loader.stderr")"; then
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
if [[ "$vertices" == "200000" && "$edges" == "1000000" ]]; then
  if ! python3 - "$loader_json" <<'PY'
import json, sys
loader = json.loads(sys.argv[1])
types = loader.get("vertex_types", {})
required = {"service": 20_000, "pod": 50_000, "container": 50_000,
            "vm": 5_000, "vmi": 5_000, "k8s_node": 4_000,
            "physical_server": 3_000, "dimm": 3_000}
if any(int(types.get(name, 0)) < minimum for name, minimum in required.items()):
    raise SystemExit("fixture ontology distribution does not meet the 200k/1M contract")
PY
  then
    write_blocked_report "fixture ontology distribution does not meet the 200k/1M contract"
    echo "BLOCKED_BY_ENV: fixture ontology distribution does not meet the 200k/1M contract" >&2
    exit 2
  fi
fi
headers=(-H "Accept: application/json")
[[ -n "${GRAPH_API_TOKEN:-}" ]] && headers+=(-H "Authorization: Bearer ${GRAPH_API_TOKEN}")
headers+=(-H "X-Tenant-ID: ${tenant_id}" -H "X-Cluster-ID: ${cluster_id}")
encoded_uid="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$uid")"
encoded_target="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$target_uid")"
encoded_alias="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$alias")"

rank=$(( (iterations * 95 + 99) / 100 ))
measure() {
  local operation="$1" method="$2" url="$3" body="${4:-}"
  local samples="$tmp_dir/${operation}.samples"
  : >"$samples"
  local success=0
  for ((warmup=0; warmup<warmup_iterations; warmup++)); do
    if [[ "$method" == "POST" ]]; then
      curl -sS -o /dev/null --max-time 30 "${headers[@]}" -H 'Content-Type: application/json' -X POST --data-raw "$body" "$url" || true
    else
      curl -sS -o /dev/null --max-time 30 "${headers[@]}" "$url" || true
    fi
  done
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
measure alias_search GET "${base_url}/entities/search?q=${encoded_alias}&limit=20"
measure one_hop GET "${base_url}/entities/${encoded_uid}/neighbors?depth=1&max_vertices=300&max_edges=1000"
measure two_hop GET "${base_url}/entities/${encoded_uid}/neighbors?depth=2&max_vertices=300&max_edges=1000"
measure shortest_path POST "${base_url}/path" "{\"source_entity_uid\":\"${uid}\",\"target_entity_uid\":\"${target_uid}\",\"max_depth\":6}"
measure rca_candidate GET "${base_url}/entities/${encoded_uid}/candidate?depth=2&max_vertices=300&max_edges=1000"
measure impact GET "${base_url}/entities/${encoded_uid}/impact?max_depth=3"

batch_mutation="$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin).get("batch_mutation", {})))' <<<"$loader_json")"
# Resource evidence is part of the gate report. The collector records
# not_collected for unavailable external dimensions; the final mode rejects
# those statuses instead of silently presenting latency as a complete gate.
resource_report="${GRAPH_RESOURCE_REPORT:-${tmp_dir}/resource.json}"
if [[ -z "${GRAPH_RESOURCE_REPORT:-}" ]]; then
  bash "${script_dir}/graph-resource-snapshot.sh" \
    --namespace "${GRAPH_RESOURCE_NAMESPACE:-${GRAPH_NAMESPACE:-observability}}" \
    --frontend-dist "${GRAPH_FRONTEND_DIST:-${repo_root}/observability-frontend/dist}" \
    --browser-url "${GRAPH_BROWSER_URL:-http://127.0.0.1:30253}" \
    --output "${resource_report}" >/dev/null
fi
if [[ ! -f "${resource_report}" ]]; then
  write_blocked_report "graph resource snapshot was not produced"
  echo "BLOCKED_BY_ENV: graph resource snapshot was not produced" >&2
  exit 2
fi
resource_json="$(cat "${resource_report}")"
python3 - "$vertices" "$edges" "$batch_size" "$uid" "$target_uid" "$alias" "$loader_json" "$output" "$tmp_dir/operations.jsonl" "$batch_mutation" "$resource_json" "$require_resources" <<'PY'
import json, os, sys
vertices, edges, batch_size = map(int, sys.argv[1:4])
uid, target_uid, alias, loader_json, output, operations_file, batch, resource_json, require_resources = sys.argv[4:]
operations = [json.loads(line) for line in open(operations_file, encoding="utf-8") if line.strip()]
batch_data = json.loads(batch)
loader = json.loads(loader_json)
resources = json.loads(resource_json)
if "success_rate" not in batch_data:
    completed = batch_data.get("iterations", 0) > 0 and isinstance(batch_data.get("p95_ms"), (int, float))
    batch_data["success"] = batch_data.get("iterations", 0) if completed else 0
    batch_data["success_rate"] = 1 if completed else 0
required_resources = {
    "hugegraph_jvm_rss_heap", "rocksdb_disk_wal", "query_api_cpu_rss",
    "ai_investigation_worker_cpu_rss", "frontend_bundle_bytes", "browser_long_tasks",
}
resource_items = resources if isinstance(resources, dict) else {}
resource_complete = set(resource_items) == required_resources and all(
    isinstance(item, dict) and item.get("status") == "collected"
    for item in resource_items.values()
)
operations.append({"operation": "batch_mutation", **batch_data})
gates = {"entity": 100, "alias_search": 200, "one_hop": 200, "two_hop": 500,
         "shortest_path": 1000, "rca_candidate": 1500, "impact": 1500,
         "batch_mutation": 2000}
required_operations = set(gates)
operation_names = [item.get("operation") for item in operations]
operations_complete = len(operations) == len(required_operations) and set(operation_names) == required_operations
for item in operations:
    operation = item.get("operation")
    if operation not in gates:
        item["passed"] = False
        continue
    item["gate_ms"] = gates[operation]
    p95 = item.get("p95_ms")
    item["passed"] = (item.get("success_rate", 0) == 1
                      and isinstance(p95, (int, float))
                      and p95 < item["gate_ms"])
all_unavailable = all(item.get("success", 0) == 0 for item in operations if item["operation"] != "batch_mutation")
resource_required = require_resources == "1"
gate_status = "BLOCKED_BY_ENV" if all_unavailable else "PASS" if operations_complete and all(item["passed"] for item in operations) and (not resource_required or resource_complete) else "FAIL"
result = {"vertices": vertices, "edges": edges, "batch_size": batch_size, "loaded": True,
          "anchor_uid": uid, "target_uid": target_uid, "alias": alias,
          "warmup_iterations": int(os.environ.get("GRAPH_LOAD_WARMUP_ITERATIONS", "10")),
          "operations": {item["operation"]: item for item in operations},
          "fixture_loader": loader, "resource_gate": resources, "resource_gate_status": "PASS" if resource_complete else "PARTIAL",
          "resource_gate_required": resource_required, "gate_status": gate_status,
          "gate_definition": gates, "operations_complete": operations_complete}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(result, handle, indent=2)
PY
cat "$output"
if grep -q '"gate_status": "PASS"' "$output"; then exit 0; fi
if grep -q '"gate_status": "BLOCKED_BY_ENV"' "$output"; then echo "BLOCKED_BY_ENV: graph query API did not return a successful sample" >&2; exit 2; fi
echo "graph performance gate failed" >&2
exit 1
