#!/usr/bin/env bash
set -euo pipefail

namespace="${GRAPH_NAMESPACE:-observability}"
output="${GRAPH_RECOVERY_OUTPUT:-/tmp/aiops-graph-recovery-report.json}"
pre_report="${GRAPH_RECOVERY_PRE_REPORT:-}"
verify_report="${GRAPH_RECOVERY_VERIFY_REPORT:-}"
query_api_deployment="${GRAPH_RECOVERY_QUERY_API_DEPLOYMENT:-query-api-http}"
inject=0
offline=0
dry_run="${GRAPH_RECOVERY_DRY_RUN:-0}"

usage() {
  cat <<'EOF'
Usage: graph-recovery-test.sh [--namespace NS] [--output PATH] [--pre-report PATH]
       [--verify-report PATH] [--inject] [--offline]

Read-only observation is the default. --inject is a destructive local/staging
drill and requires GRAPH_RECOVERY_ENV=local|staging,
GRAPH_RECOVERY_CONFIRM=I_UNDERSTAND_LOCAL_GRAPH_RECOVERY, and
GRAPH_RECOVERY_FULL_DATA_LOSS=1. GRAPH_RECOVERY_DRY_RUN=1 validates the guard
and writes a planned report without invoking kubectl or Helm.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace) namespace="${2:?--namespace requires a value}"; shift 2 ;;
    --output) output="${2:?--output requires a path}"; shift 2 ;;
    --pre-report) pre_report="${2:?--pre-report requires a path}"; shift 2 ;;
    --verify-report) verify_report="${2:?--verify-report requires a path}"; shift 2 ;;
    --inject) inject=1; shift ;;
    --offline) offline=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

[[ "${query_api_deployment}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || {
  echo "invalid GRAPH_RECOVERY_QUERY_API_DEPLOYMENT" >&2
  exit 2
}

mkdir -p "$(dirname "${output}")"
if [[ "${offline}" == "1" ]]; then
  python3 - "${output}" <<'PY'
import json, sys
json.dump({"recovery_test": "planned", "checks": [
    "processing reclaim", "outbox retry/dead", "schema mismatch pause",
    "generation stale grace", "historical RCA independence"],
    "mutation": "none", "environment": "offline"},
    open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False)
PY
  cat "${output}"
  exit 0
fi

if [[ "${inject}" == "0" ]]; then
  command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required" >&2; exit 2; }
  kubectl -n "${namespace}" get statefulset/hugegraph -o wide
  kubectl -n "${namespace}" get job/graph-schema-migrator -o jsonpath='{.status.succeeded}{"\n"}'
  kubectl -n "${namespace}" get pods -l app=hugegraph -o jsonpath='{range .items[*]}{.metadata.name}{" ready="}{.status.containerStatuses[0].ready}{"\n"}{end}'
  python3 - "${output}" "${namespace}" <<'PY'
import json, sys
json.dump({"recovery_test": "observed", "failure_injection": "not_run",
           "mutation": "none", "namespace": sys.argv[2]},
          open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False)
PY
  cat "${output}"
  exit 0
fi

recovery_env="${GRAPH_RECOVERY_ENV:-}"
confirm="${GRAPH_RECOVERY_CONFIRM:-}"
context="${GRAPH_RECOVERY_CONTEXT:-}"
if [[ "${recovery_env}" != "local" && "${recovery_env}" != "staging" ]]; then
  echo "BLOCKED_BY_ENV: --inject requires GRAPH_RECOVERY_ENV=local or staging" >&2
  exit 2
fi
if [[ "${confirm}" != "I_UNDERSTAND_LOCAL_GRAPH_RECOVERY" ]]; then
  echo "BLOCKED_BY_ENV: --inject requires explicit recovery confirmation" >&2
  exit 2
fi
if [[ "${namespace}" =~ (^|[-_])(prod|production|live)([-_]|$) ]]; then
  echo "BLOCKED_BY_ENV: refusing recovery injection in production-like namespace" >&2
  exit 2
fi
if [[ "${dry_run}" == "1" ]]; then
  python3 - "${output}" "${recovery_env}" "${namespace}" <<'PY'
import json, sys
json.dump({"recovery_test": "dry_run", "mutation": "planned",
           "environment": sys.argv[2], "namespace": sys.argv[3],
           "checks": ["guard validation", "PVC deletion plan", "Helm rebuild plan",
                       "schema migration", "source reconcile", "RCA history"]},
          open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
  cat "${output}"
  exit 0
fi
if [[ "${context}" == "" ]]; then
  command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required" >&2; exit 2; }
  context="$(kubectl config current-context 2>/dev/null || true)"
fi
if [[ "${context}" =~ (^|[-_])(prod|production|live)([-_]|$) ]]; then
  echo "BLOCKED_BY_ENV: refusing recovery injection in production-like context" >&2
  exit 2
fi
if [[ "${GRAPH_RECOVERY_FULL_DATA_LOSS:-0}" != "1" ]]; then
  echo "BLOCKED_BY_ENV: set GRAPH_RECOVERY_FULL_DATA_LOSS=1 for the destructive drill" >&2
  exit 2
fi
if [[ -z "${pre_report}" || ! -f "${pre_report}" ]]; then
  echo "BLOCKED_BY_ENV: --inject requires a pre-recovery vertex/edge/RCA report" >&2
  exit 2
fi
command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required" >&2; exit 2; }
command -v helm >/dev/null 2>&1 || { echo "helm is required" >&2; exit 2; }
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

pre_state="$(kubectl -n "${namespace}" get statefulset/hugegraph,pvc/hugegraph-data -o json 2>/dev/null || true)"
[[ -n "${pre_state}" ]] || { echo "recovery target HugeGraph StatefulSet/PVC was not found" >&2; exit 1; }
kubectl -n "${namespace}" scale "deployment/${query_api_deployment}" --replicas=0
kubectl -n "${namespace}" scale statefulset/hugegraph --replicas=0
kubectl -n "${namespace}" wait --for=delete pod -l app=hugegraph --timeout="${GRAPH_RECOVERY_WAIT_TIMEOUT:-15m}"
kubectl -n "${namespace}" delete pvc/hugegraph-data --wait=true
kubectl -n "${namespace}" delete job/graph-schema-migrator --ignore-not-found=true
helm upgrade aiops "${repo_root}/deploy/helm/aiops" -n "${namespace}" --reuse-values --wait --timeout "${GRAPH_RECOVERY_HELM_TIMEOUT:-15m}"
kubectl -n "${namespace}" wait --for=condition=ready pod -l app=hugegraph --timeout="${GRAPH_RECOVERY_WAIT_TIMEOUT:-15m}"
kubectl -n "${namespace}" wait --for=condition=complete job/graph-schema-migrator --timeout="${GRAPH_RECOVERY_WAIT_TIMEOUT:-15m}"
kubectl -n "${namespace}" scale "deployment/${query_api_deployment}" --replicas="${GRAPH_RECOVERY_QUERY_API_REPLICAS:-1}"
kubectl -n "${namespace}" rollout status "deployment/${query_api_deployment}" --timeout="${GRAPH_RECOVERY_WAIT_TIMEOUT:-15m}"

graph_ready="false"
if kubectl -n "${namespace}" exec "deploy/${query_api_deployment}" -- wget --no-check-certificate -q -O - https://127.0.0.1:8080/readyz >/dev/null 2>&1 \
  && kubectl -n "${namespace}" exec "deploy/${query_api_deployment}" -- sh -c 'AUTH="$(printf "%s:%s" "$HUGEGRAPH_USERNAME" "$HUGEGRAPH_PASSWORD" | base64 | tr -d "\\n")"; wget -q --header="Authorization: Basic ${AUTH}" -O- http://hugegraph:8080/graphs >/dev/null' 2>/dev/null; then
  graph_ready="true"
fi
reconcile_status="not_configured"
if [[ -n "${GRAPH_RECOVERY_RECONCILE_COMMAND:-}" ]]; then
  bash -c "${GRAPH_RECOVERY_RECONCILE_COMMAND}"
  reconcile_status="success"
fi
if [[ -z "${verify_report}" || ! -f "${verify_report}" ]]; then
  echo "BLOCKED_BY_ENV: --inject requires a post-recovery verification report" >&2
  exit 2
fi
python3 - "${output}" "${namespace}" "${context}" "${graph_ready}" "${reconcile_status}" "${pre_report}" "${verify_report}" <<'PY'
import json, sys
output, namespace, context, graph_ready, reconcile_status, pre_path, verify_path = sys.argv[1:]
pre = json.load(open(pre_path, encoding="utf-8"))
verify = json.load(open(verify_path, encoding="utf-8"))
checks = {
    "pre_state_captured": all(key in pre for key in ("vertex_count", "edge_count", "historical_rca_ids")),
    "schema_migration": verify.get("schema_checksum_match") is True,
    "historical_rca": verify.get("historical_rca") is True,
    "identity": verify.get("identity_match") is True,
    "edges": verify.get("edge_identity_match") is True,
    "alias_conflict": verify.get("alias_conflict", 1) == 0,
    "outbox": verify.get("dead_outbox", 1) == 0,
    "source_sync": verify.get("source_sync") == "success",
}
result = {"recovery_test": "executed", "mutation": "performed", "namespace": namespace,
          "context": context, "graph_ready_during_rebuild": False,
          "graph_ready_after_rebuild": graph_ready == "true", "reconcile_status": reconcile_status,
          "pre_recovery": {"vertex_count": pre.get("vertex_count"), "edge_count": pre.get("edge_count"),
                           "historical_rca_count": len(pre.get("historical_rca_ids", []))},
          "post_recovery": verify, "checks": checks}
result["gate_status"] = "PASS" if result["reconcile_status"] == "success" and result["graph_ready_after_rebuild"] and all(checks.values()) else "FAIL"
json.dump(result, open(output, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps(result, ensure_ascii=False, indent=2))
if result["gate_status"] != "PASS":
    raise SystemExit(1)
PY
