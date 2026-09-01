#!/usr/bin/env bash
set -euo pipefail

# One-shot 200k/1M capacity gate. This is intentionally separate from
# graph-load-test.sh: it performs no warmup, repetition, latency sampling or
# pressure test. The gate proves a real ontology load, Query-owned alias
# projection, one read of each public operation and a complete resource
# snapshot.
vertices="${GRAPH_CAPACITY_VERTICES:-200000}"
edges="${GRAPH_CAPACITY_EDGES:-1000000}"
batch_size="${GRAPH_CAPACITY_BATCH_SIZE:-500}"
tenant_id="${GRAPH_CAPACITY_TENANT_ID:-${GRAPH_API_TENANT_ID:-}}"
cluster_id="${GRAPH_CAPACITY_CLUSTER_ID:-${GRAPH_API_CLUSTER_ID:-}}"
api_base="${GRAPH_API_BASE_URL:-https://127.0.0.1:18081/api/v1/ai/kg}"
alias="${GRAPH_CAPACITY_ALIAS:-graph-load-service-000000}"
uid="${GRAPH_CAPACITY_UID:-loadtest:vertex:000000}"
target_uid="${GRAPH_CAPACITY_TARGET_UID:-loadtest:vertex:000001}"
output="${GRAPH_CAPACITY_OUTPUT:-/tmp/aiops-graph-capacity-gate.json}"
resource_output="${GRAPH_CAPACITY_RESOURCE_OUTPUT:-/tmp/aiops-graph-resource-capacity.json}"
cookie_file="${GRAPH_API_COOKIE_FILE:-}"
insecure="${GRAPH_API_INSECURE_SKIP_VERIFY:-1}"

[[ "$vertices" == "200000" && "$edges" == "1000000" ]] || {
  echo "graph capacity gate requires exactly 200000 vertices and 1000000 edges" >&2
  exit 2
}
[[ "$batch_size" =~ ^[1-9][0-9]*$ && "$batch_size" -le 500 ]] || {
  echo "GRAPH_CAPACITY_BATCH_SIZE must be 1..500" >&2
  exit 2
}
[[ -n "$tenant_id" && -n "$cluster_id" ]] || {
  echo "GRAPH_CAPACITY_TENANT_ID and GRAPH_CAPACITY_CLUSTER_ID are required" >&2
  exit 2
}
[[ -n "${HUGEGRAPH_URL:-}" ]] || {
  echo "HUGEGRAPH_URL is required for the real capacity write" >&2
  exit 2
}
[[ -n "${MYSQL_HOST:-}" && -n "${MYSQL_USER:-}" && -n "${MYSQL_DB:-}" ]] || {
  echo "MYSQL_HOST, MYSQL_USER and MYSQL_DB are required for Query-owned alias projection" >&2
  exit 2
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-graph-capacity.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

if [[ -n "${GRAPH_LOAD_GENERATOR_CMD:-}" ]]; then
  read -r -a loader_cmd <<<"${GRAPH_LOAD_GENERATOR_CMD}"
else
  loader_cmd=(go run ./cmd/graph-load-generator)
fi
loader_args=(--vertices "$vertices" --edges "$edges" --batch-size "$batch_size"
  --tenant-id "$tenant_id" --cluster-id "$cluster_id" --load=true
  --batch-benchmark-iterations 0 --project-query-aliases)
loader_json="$(cd "${repo_root}/ai-apm-query-go" && "${loader_cmd[@]}" "${loader_args[@]}" 2>"${tmp_dir}/loader.stderr")" || {
  cat "${tmp_dir}/loader.stderr" >&2 || true
  echo "graph capacity gate: real fixture load failed" >&2
  exit 1
}

encoded_uid="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$uid")"
encoded_target="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$target_uid")"
encoded_alias="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$alias")"
curl_args=(-sS --max-time 30)
[[ "$insecure" == "1" || "$insecure" == "true" ]] && curl_args+=(-k)
[[ -n "$cookie_file" ]] && curl_args+=(-b "$cookie_file")
[[ -n "${GRAPH_API_TOKEN:-}" ]] && curl_args+=(-H "Authorization: Bearer ${GRAPH_API_TOKEN}")
# Tenant identity comes from the authenticated MySQL-backed session. The
# cluster reference is only a selector and is re-resolved/authorized by the
# Query API; do not resurrect the retired caller-controlled tenant header.
curl_args+=(-H "Accept: application/json" -H "X-Cluster-ID: ${cluster_id}")

request_once() {
  local name="$1" method="$2" url="$3" body="${4:-}"
  local body_file="${tmp_dir}/${name}.json" status
  if [[ "$method" == "POST" ]]; then
    status="$(curl "${curl_args[@]}" -o "$body_file" -w '%{http_code}' -H 'Content-Type: application/json' -X POST --data-raw "$body" "$url" || true)"
  else
    status="$(curl "${curl_args[@]}" -o "$body_file" -w '%{http_code}' "$url" || true)"
  fi
  printf '%s\t%s\n' "$status" "$body_file" >"${tmp_dir}/${name}.meta"
}

request_once health GET "${api_base%/}/health"
request_once entity GET "${api_base%/}/entities/${encoded_uid}"
request_once alias_search GET "${api_base%/}/entities/search?q=${encoded_alias}&limit=5"
request_once neighbors GET "${api_base%/}/entities/${encoded_uid}/neighbors?depth=1&max_vertices=300&max_edges=1000"
request_once candidate GET "${api_base%/}/entities/${encoded_uid}/candidate?depth=1&max_vertices=300&max_edges=1000"
request_once impact GET "${api_base%/}/entities/${encoded_uid}/impact?max_depth=1&max_vertices=300&max_edges=1000"
request_once path POST "${api_base%/}/path" "{\"source_entity_uid\":\"${uid}\",\"target_entity_uid\":\"${target_uid}\",\"max_depth\":3}"

GRAPH_RESOURCE_OUTPUT="$resource_output" bash "${script_dir}/graph-resource-snapshot.sh" \
  --namespace "${GRAPH_RESOURCE_NAMESPACE:-${GRAPH_NAMESPACE:-observability}}" \
  --frontend-dist "${GRAPH_FRONTEND_DIST:-${repo_root}/observability-frontend/dist}" \
  --browser-url "${GRAPH_BROWSER_URL:-http://127.0.0.1:30253}" >/dev/null

python3 - "$loader_json" "$output" "$resource_output" "$tmp_dir" "$vertices" "$edges" "$batch_size" "$tenant_id" "$cluster_id" "$uid" "$target_uid" "$alias" <<'PY'
import json, pathlib, sys
loader_raw, output, resource_path, tmp_dir, vertices, edges, batch_size, tenant, cluster, uid, target_uid, alias = sys.argv[1:]
loader = json.loads(loader_raw)
resources = json.load(open(resource_path, encoding="utf-8"))
operations = {}
for meta in pathlib.Path(tmp_dir).glob("*.meta"):
    name = meta.stem
    status, body_path = meta.read_text(encoding="utf-8").split("\t", 1)
    body = {}
    try: body = json.load(open(body_path.strip(), encoding="utf-8"))
    except Exception: pass
    operations[name] = {"status": int(status) if status.isdigit() else 0, "body": body}
required_operations = {"health", "entity", "alias_search", "neighbors", "candidate", "impact", "path"}
resource_complete = set(resources) == {
    "hugegraph_jvm_rss_heap", "rocksdb_disk_wal", "query_api_cpu_rss",
    "ai_investigation_worker_cpu_rss", "frontend_bundle_bytes", "browser_long_tasks",
} and all(isinstance(item, dict) and item.get("status") == "collected" for item in resources.values())
loader_ok = (loader.get("loaded") is True and loader.get("vertices") == int(vertices)
             and loader.get("edges") == int(edges)
             and loader.get("alias_projection_enabled") is True
             and loader.get("aliases_projected") == int(vertices))
operations_ok = set(operations) == required_operations and all(item.get("status") == 200 for item in operations.values())
alias_ok = operations.get("alias_search", {}).get("body", {}).get("count", 0) >= 1
result = {
    "vertices": int(vertices), "edges": int(edges), "batch_size": int(batch_size),
    "tenant_id": tenant, "cluster_id": cluster, "anchor_uid": uid, "target_uid": target_uid,
    "alias": alias, "pressure_test": False, "benchmark_iterations": 0,
    "fixture_loader": loader, "operations": operations, "resource_gate": resources,
    "resource_gate_status": "PASS" if resource_complete else "PARTIAL",
    "checks": {"loader_and_projection": loader_ok, "single_read_operations": operations_ok,
                "alias_search_result": alias_ok, "resources": resource_complete},
}
result["gate_status"] = "PASS" if all(result["checks"].values()) else "BLOCKED_BY_ENV"
json.dump(result, open(output, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps({"gate_status": result["gate_status"], "loader_and_projection": loader_ok,
                  "single_read_operations": operations_ok, "alias_search_result": alias_ok,
                  "resource_gate_status": result["resource_gate_status"]}, ensure_ascii=False))
if result["gate_status"] != "PASS": raise SystemExit(2)
PY
