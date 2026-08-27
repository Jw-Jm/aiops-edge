#!/usr/bin/env bash
set -euo pipefail

# Bounded Graph API smoke/latency gate. It never writes graph data.
base_url="${GRAPH_API_BASE_URL:-http://127.0.0.1:8080/api/v1/ai/kg}"
uid="${GRAPH_TEST_ENTITY_UID:-}"
iterations="${GRAPH_LOAD_ITERATIONS:-20}"
output="${GRAPH_LOAD_REPORT:-/tmp/aiops-graph-load-report.json}"
usage() { echo "Usage: graph-load-test.sh [--uid ENTITY_UID] [--iterations N] [--base-url URL] [--output PATH]"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --uid) uid="${2:?--uid requires a value}"; shift 2 ;;
    --iterations) iterations="${2:?--iterations requires a value}"; shift 2 ;;
    --base-url) base_url="${2:?--base-url requires a value}"; shift 2 ;;
    --output) output="${2:?--output requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 2; }
[[ "${iterations}" =~ ^[1-9][0-9]*$ ]] || { echo "iterations must be positive" >&2; exit 2; }
if [[ -z "${uid}" ]]; then
  echo "BLOCKED_BY_ENV: GRAPH_TEST_ENTITY_UID is required for a live graph traversal" >&2
  exit 2
fi
tmp="$(mktemp "${TMPDIR:-/tmp}/aiops-graph-load.XXXXXX")"
trap 'rm -f "${tmp}"' EXIT
total_ms=0
max_ms=0
ok=0
for ((i=0; i<iterations; i++)); do
  start="$(python3 -c 'import time; print(time.perf_counter_ns())')"
  encoded_uid="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "${uid}")"
  status="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "${base_url}/entities/${encoded_uid}/neighbors?depth=1&max_vertices=50&max_edges=100" || true)"
  end="$(python3 -c 'import time; print(time.perf_counter_ns())')"
  elapsed=$(( (end-start)/1000000 ))
  total_ms=$((total_ms+elapsed))
  if (( elapsed > max_ms )); then max_ms=${elapsed}; fi
  if [[ "${status}" == "200" ]]; then ok=$((ok+1)); fi
  echo "${elapsed}" >>"${tmp}"
done
avg_ms=$((total_ms/iterations))
p95_ms="$(sort -n "${tmp}" | awk -v n="${iterations}" 'BEGIN{p=int(n*0.95); if(p<1)p=1} NR==p{print; found=1} END{if(!found)print 0}')"
python3 -c 'import json,sys; json.dump({"iterations":int(sys.argv[1]),"success":int(sys.argv[2]),"success_rate":int(sys.argv[2])/int(sys.argv[1]),"avg_ms":int(sys.argv[3]),"p95_ms":int(sys.argv[4]),"max_ms":int(sys.argv[5]),"endpoint":sys.argv[6]},open(sys.argv[7],"w"),indent=2)' "${iterations}" "${ok}" "${avg_ms}" "${p95_ms}" "${max_ms}" "${base_url}" "${output}"
cat "${output}"
[[ "${ok}" -eq "${iterations}" ]] || { echo "graph load gate failed" >&2; exit 1; }
