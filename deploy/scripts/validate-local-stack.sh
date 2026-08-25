#!/usr/bin/env bash
set -euo pipefail

# Read-only post-deploy validator. It never changes EXECUTION_MODE, namespaces,
# workloads or data. Environment-only checks are reported as BLOCKED_BY_ENV.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART_DIR="${ROOT}/deploy/helm/aiops"
OFFLINE=0
for arg in "$@"; do
  case "$arg" in
    --offline) OFFLINE=1 ;;
    -h|--help) echo "Usage: validate-local-stack.sh [--offline]"; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 2; }
}
require_cmd helm
require_cmd rg

RELEASE_SHA="$(git -C "${ROOT}" rev-parse HEAD)"
RELEASE_TAG="${RELEASE_TAG:-git-${RELEASE_SHA:0:12}}"
echo "[validator] release=${RELEASE_SHA} tag=${RELEASE_TAG} offline=${OFFLINE}"

if [[ "${OFFLINE}" == "1" ]]; then
  echo "[validator] Helm lint and deployment contracts"
  helm lint "${CHART_DIR}"
  LLM_PROVIDER_KEYS="deepseek:sk-contract-only" bash "${ROOT}/deploy/scripts/secret-format-test.sh"
  bash "${ROOT}/deploy/scripts/test-deployment-contracts.sh"
  echo "BLOCKED_BY_ENV: live Kubernetes readiness/data/LLM/DeepFlow checks were not run (--offline)"
  exit 0
fi

require_cmd kubectl
for ns in observability aiops-canary; do
  kubectl get namespace "${ns}" >/dev/null
done

echo "[validator] core workload readiness"
for selector in \
  app=query-api \
  app=ai-investigation-worker \
  app=ai-llm-egress-proxy \
  app=ingest \
  app=event-collector \
  app=frontend
do
  if ! kubectl -n observability wait --for=condition=ready pod -l "${selector}" --timeout=180s; then
    echo "core readiness failed for ${selector}" >&2
    exit 1
  fi
done
kubectl -n observability wait --for=condition=ready pod -l app=mysql --timeout=300s

echo "[validator] Query API readiness endpoint"
kubectl -n observability get pods -l app=query-api -o wide
if ! kubectl -n observability exec deploy/query-api -- wget -q -O - http://127.0.0.1:8080/readyz >/dev/null; then
  echo "Query API /readyz failed" >&2
  exit 1
fi

echo "[validator] MySQL schema and migration accounts"
root_password="$(kubectl -n observability get secret aiops-secrets -o jsonpath='{.data.MYSQL_ROOT_PASSWORD}' | base64 -d)"
app_password="$(kubectl -n observability get secret aiops-secrets -o jsonpath='{.data.MYSQL_APP_PASSWORD}' | base64 -d)"
migrator_password="$(kubectl -n observability get secret aiops-secrets -o jsonpath='{.data.MYSQL_MIGRATOR_PASSWORD}' | base64 -d)"
[[ -n "${app_password}" && -n "${migrator_password}" ]] || {
  echo "MYSQL_APP_PASSWORD and MYSQL_MIGRATOR_PASSWORD must both be present" >&2
  exit 1
}
[[ "${app_password}" != "${migrator_password}" ]] || {
  echo "MYSQL_APP_PASSWORD and MYSQL_MIGRATOR_PASSWORD must be independent" >&2
  exit 1
}
schema_rows="$(kubectl -n observability exec statefulset/mysql -- env MYSQL_PWD="${root_password}" mysql -uroot -N -e \
  "SELECT migration_id FROM aiops.aiops_schema_migrations ORDER BY migration_id;")"
for version in 0001 0002 0003 0004 0005 0006 0007 0008 0009; do
  if ! rg -n --fixed-strings "mysql/${version}" <<<"${schema_rows}" >/dev/null; then
    echo "schema migration ${version} is missing" >&2
    exit 1
  fi
done
if ! rg -n --fixed-strings "mysql/0009_action_workflow_closure" <<<"${schema_rows}" >/dev/null; then
  echo "0009_action_workflow_closure is missing" >&2
  exit 1
fi
printf '%s\n' "${schema_rows}"
kubectl -n observability exec statefulset/mysql -- env MYSQL_PWD="${root_password}" mysql -uroot -N -e \
  "SHOW GRANTS FOR 'aiops_app'@'%'; SHOW GRANTS FOR 'aiops_migrator'@'%';"

echo "[validator] Executor disabled safety boundary"
executor_env="$(kubectl -n observability get deployment ai-action-executor -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' 2>/dev/null || true)"
[[ -n "${executor_env}" ]] || { echo "Executor deployment is missing" >&2; exit 1; }
echo "${executor_env}"
rg -n '^EXECUTION_MODE=disabled$' <<<"${executor_env}" >/dev/null || { echo "Executor is not disabled during base validation" >&2; exit 1; }
rg -n '^POD_SA_ACCESS=false$' <<<"${executor_env}" >/dev/null || { echo "Executor POD_SA_ACCESS is not false during base validation" >&2; exit 1; }
if kubectl -n observability get role ai-action-executor >/dev/null 2>&1; then
  echo "Executor Role unexpectedly exists in observability" >&2
  exit 1
fi

echo "[validator] canonical Worker switches and LLM proxy readiness"
worker_env="$(kubectl -n observability get deployment ai-investigation-worker -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}')"
rg -n '^INVESTIGATION_RUNTIME_ENABLED=1$' <<<"${worker_env}" >/dev/null || { echo "Worker runtime switch is not 1" >&2; exit 1; }
for switch in LEGACY_FLOW_RUNTIME_ENABLED LEGACY_DIRECT_MUTATIONS_ENABLED; do
  rg -n "^${switch}=0$" <<<"${worker_env}" >/dev/null || { echo "Worker ${switch} is not 0" >&2; exit 1; }
done
kubectl -n observability exec deploy/ai-llm-egress-proxy -- wget -q -O - http://127.0.0.1:8080/readyz >/dev/null
proxy_env="$(kubectl -n observability get deployment ai-llm-egress-proxy -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}')"
if awk -F= '$1 ~ /(PROVIDER|API_KEY)/ && length($2) > 0 { found=1 } END { exit(found ? 0 : 1) }' <<<"${proxy_env}" || \
   rg -n 'sk-[^[:space:]]+' <<<"${proxy_env}" >/dev/null; then
  echo "provider credentials were rendered as plaintext env values" >&2
  exit 1
fi

echo "[validator] RBAC least privilege"
executor_subject="system:serviceaccount:observability:ai-action-executor"
if rg -n '^POD_SA_ACCESS=true$' <<<"${executor_env}" >/dev/null; then
  [[ "$(kubectl auth can-i get deployments -n aiops-canary --as="${executor_subject}")" == "yes" ]] || {
    echo "Approved executor must be able to get canary deployments" >&2
    exit 1
  }
  [[ "$(kubectl auth can-i patch deployments -n aiops-canary --as="${executor_subject}")" == "yes" ]] || {
    echo "Approved executor must be able to patch canary deployments" >&2
    exit 1
  }
  if [[ "$(kubectl auth can-i delete deployments -n aiops-canary --as="${executor_subject}")" != "no" ]]; then
    echo "Executor delete permission must be no" >&2
    exit 1
  fi
else
  [[ "$(kubectl auth can-i get deployments -n aiops-canary --as="${executor_subject}")" == "no" ]] || {
    echo "Disabled executor must not have canary read permission" >&2
    exit 1
  }
  [[ "$(kubectl auth can-i patch deployments -n aiops-canary --as="${executor_subject}")" == "no" ]] || {
    echo "Disabled executor must not have canary patch permission" >&2
    exit 1
  }
  echo "disabled executor has no canary RBAC permissions"
fi
if [[ "$(kubectl auth can-i patch deployments -n observability --as=system:serviceaccount:observability:ai-action-executor)" != "no" ]]; then
  echo "Executor cross-namespace patch permission must be no" >&2
  exit 1
fi
if [[ "$(kubectl auth can-i patch deployments -n aiops-canary --as=system:serviceaccount:observability:ai-orchestrator)" != "no" ]]; then
  echo "Orchestrator must not have mutation permission" >&2
  exit 1
fi

echo "[validator] canary object"
kubectl -n aiops-canary rollout status deployment/aiops-mutation-canary --timeout=180s
kubectl -n aiops-canary get deployment aiops-mutation-canary -o jsonpath='{.metadata.uid}{" "}{.metadata.resourceVersion}{"\n"}'

if [[ -z "${AIOPS_VALIDATION_DATA_MARKER:-}" ]]; then
  echo "BLOCKED_BY_ENV: real metric/log/event markers were not supplied (set AIOPS_VALIDATION_DATA_MARKER after creating them)"
else
  echo "BLOCKED_BY_ENV: marker ${AIOPS_VALIDATION_DATA_MARKER} requires Query API/Frontend evidence capture"
fi
echo "BLOCKED_BY_ENV: real provider response, DeepFlow flow/span, multi-node failover, PITR and Credential Broker gates require environment evidence"
echo "local stack structural/readiness validation passed; environment-gated evidence remains blocked"
