#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT_DIR="${ROOT}/deploy/scripts"
CHART_DIR="${ROOT}/deploy/helm/aiops"
NAMESPACE="observability"
CANARY_NAMESPACE="aiops-canary"
DEEPFLOW_NAMESPACE="deepflow"
DRY_RUN=0
DESTROY=0
CONFIRM_DESTROY=0
SKIP_BUILD="${SKIP_IMAGE_BUILD:-0}"
SKIP_DEEPFLOW="${SKIP_DEEPFLOW:-0}"
SECRET_FILE="${AIOPS_SECRET_FILE:-}"

usage() {
  cat <<'EOF'
Usage: local-validation.sh [options]

  --dry-run          Print the ordered validation plan without changing state.
  --destroy          Delete local observability/deepflow/canary namespaces first.
  --confirm-destroy  Required together with --destroy.
  --secret-file PATH Source generated shell secret file instead of generating one.
  --skip-build       Skip Docker image builds.
  --skip-deepflow    Skip DeepFlow and report BLOCKED_BY_ENV.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=1; shift ;;
    --destroy) DESTROY=1; shift ;;
    --confirm-destroy) CONFIRM_DESTROY=1; shift ;;
    --secret-file) SECRET_FILE="${2:?--secret-file requires a path}"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    --skip-deepflow) SKIP_DEEPFLOW=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "${DESTROY}" == "1" && "${CONFIRM_DESTROY}" != "1" ]]; then
  echo "--destroy requires --confirm-destroy; refusing destructive operation" >&2
  exit 2
fi

step() { echo "[$1] $2"; }
run() {
  printf '+ '
  printf '%q ' "$@"
  printf '\n'
  [[ "${DRY_RUN}" == "1" ]] || "$@"
}

if [[ "${DRY_RUN}" == "1" ]]; then
  step 0 "generate secrets (requires explicit LLM_PROVIDER_KEYS)"
  step 1 "build all self-owned images with RELEASE_TAG=git-<SHA>"
  step 2 "helm lint/template and deployment contract gates"
  step 3 "bootstrap Helm install: MySQL/ClickHouse/VM/VLogs only"
  step 4 "wait mysql-users-init and mysql-init schema migrator"
  step 5 "runtime Helm upgrade: Query API/Worker/Proxy/ingest/frontend"
  step 6 "canary health, real data, RBAC and disabled Executor checks"
  if [[ "${SKIP_DEEPFLOW}" == "1" ]]; then
    echo "BLOCKED_BY_ENV: DeepFlow installation was explicitly skipped"
  else
    step 7 "DeepFlow install and flow/span validation"
  fi
  exit 0
fi

for command in git helm kubectl; do
  command -v "${command}" >/dev/null 2>&1 || { echo "missing command: ${command}" >&2; exit 2; }
done

RELEASE_SHA="$(git -C "${ROOT}" rev-parse HEAD)"
RELEASE_TAG="${RELEASE_TAG:-git-${RELEASE_SHA:0:12}}"
if [[ ! "${RELEASE_TAG}" =~ ^git-[0-9a-f]{12}$ ]]; then
  echo "RELEASE_TAG must be git-<12 hex SHA>, got ${RELEASE_TAG}" >&2
  exit 2
fi

step 0 "generate or load secrets"
if [[ -n "${SECRET_FILE}" ]]; then
  [[ -f "${SECRET_FILE}" ]] || { echo "secret file not found: ${SECRET_FILE}" >&2; exit 1; }
else
  SECRET_FILE="${TMPDIR:-/tmp}/aiops-local-secrets-${RELEASE_TAG}.env"
  if [[ ! -f "${SECRET_FILE}" ]]; then
    "${SCRIPT_DIR}/generate-local-secrets.sh" --output "${SECRET_FILE}"
  fi
fi
# shellcheck disable=SC1090
source "${SECRET_FILE}"
for required in JWT_SECRET LLM_ENCRYPTION_KEY INTERNAL_TOKEN INGEST_API_KEY \
  CLICKHOUSE_PASSWORD MYSQL_ROOT_PASSWORD MYSQL_APP_PASSWORD MYSQL_MIGRATOR_PASSWORD \
  LLM_PROXY_TOKEN LLM_PROVIDER_KEYS ORCHESTRATOR_TO_QUERY_TOKEN \
  ORCHESTRATOR_TO_QUERY_SIGNING_KEY ORCHESTRATOR_TO_QUERY_VERIFY_KEYS \
  QUERY_TO_ORCHESTRATOR_TOKEN QUERY_TO_ORCHESTRATOR_SIGNING_KEY \
  QUERY_TO_ORCHESTRATOR_VERIFY_KEYS EXECUTOR_TOKEN \
  AI_ACTION_EXECUTOR_SIGNING_KEY AI_ACTION_EXECUTOR_VERIFY_KEYS
do
  [[ -n "${!required:-}" ]] || { echo "missing secret variable: ${required}" >&2; exit 1; }
done

SECRET_VALUES="${TMPDIR:-/tmp}/aiops-local-values-${RELEASE_TAG}.yaml"
CANARY_MANIFEST="${TMPDIR:-/tmp}/aiops-canary-${RELEASE_TAG}.yaml"
trap 'rm -f "${SECRET_VALUES}" "${CANARY_MANIFEST}"' EXIT
yaml_quote() { printf "'%s'" "${1//\'/\'\'}"; }
{
  echo "secrets:"
  printf '  jwtSecret: %s\n' "$(yaml_quote "${JWT_SECRET}")"
  printf '  llmEncryptionKey: %s\n' "$(yaml_quote "${LLM_ENCRYPTION_KEY}")"
  printf '  internalToken: %s\n' "$(yaml_quote "${INTERNAL_TOKEN}")"
  printf '  ingestApiKey: %s\n' "$(yaml_quote "${INGEST_API_KEY}")"
  printf '  clickhousePassword: %s\n' "$(yaml_quote "${CLICKHOUSE_PASSWORD}")"
  printf '  mysqlRootPassword: %s\n' "$(yaml_quote "${MYSQL_ROOT_PASSWORD}")"
  printf '  mysqlAppPassword: %s\n' "$(yaml_quote "${MYSQL_APP_PASSWORD}")"
  printf '  mysqlMigratorPassword: %s\n' "$(yaml_quote "${MYSQL_MIGRATOR_PASSWORD}")"
  printf '  adminInitialPassword: %s\n' "$(yaml_quote "${ADMIN_INITIAL_PASSWORD:-}")"
  printf '  llmProxyToken: %s\n' "$(yaml_quote "${LLM_PROXY_TOKEN}")"
  printf '  llmProviderKeys: %s\n' "$(yaml_quote "${LLM_PROVIDER_KEYS}")"
  printf '  orchestratorToQueryToken: %s\n' "$(yaml_quote "${ORCHESTRATOR_TO_QUERY_TOKEN}")"
  printf '  orchestratorToQuerySigningKey: %s\n' "$(yaml_quote "${ORCHESTRATOR_TO_QUERY_SIGNING_KEY}")"
  printf '  orchestratorToQueryVerifyKeys: %s\n' "$(yaml_quote "${ORCHESTRATOR_TO_QUERY_VERIFY_KEYS}")"
  printf '  queryToOrchestratorToken: %s\n' "$(yaml_quote "${QUERY_TO_ORCHESTRATOR_TOKEN}")"
  printf '  queryToOrchestratorSigningKey: %s\n' "$(yaml_quote "${QUERY_TO_ORCHESTRATOR_SIGNING_KEY}")"
  printf '  queryToOrchestratorVerifyKeys: %s\n' "$(yaml_quote "${QUERY_TO_ORCHESTRATOR_VERIFY_KEYS}")"
  printf '  executorToken: %s\n' "$(yaml_quote "${EXECUTOR_TOKEN}")"
  printf '  aiActionExecutorSigningKey: %s\n' "$(yaml_quote "${AI_ACTION_EXECUTOR_SIGNING_KEY}")"
  printf '  aiActionExecutorVerifyKeys: %s\n' "$(yaml_quote "${AI_ACTION_EXECUTOR_VERIFY_KEYS}")"
} >"${SECRET_VALUES}"
chmod 600 "${SECRET_VALUES}"

if [[ "${DESTROY}" == "1" ]]; then
  step 0.1 "destroy old local namespaces and PVCs"
  for namespace in "${NAMESPACE}" "${DEEPFLOW_NAMESPACE}" "${CANARY_NAMESPACE}"; do
    case "${namespace}" in
      observability|deepflow|aiops-canary) ;;
      *) echo "refusing to delete unexpected namespace ${namespace}" >&2; exit 2 ;;
    esac
    run kubectl delete namespace "${namespace}" --ignore-not-found --wait=true
  done
fi

step 1 "create canary namespace and workload"
if ! kubectl get namespace "${CANARY_NAMESPACE}" >/dev/null 2>&1; then
  run kubectl create namespace "${CANARY_NAMESPACE}"
fi
cat >"${CANARY_MANIFEST}" <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aiops-mutation-canary
  namespace: aiops-canary
  labels:
    app: aiops-mutation-canary
spec:
  replicas: 1
  selector:
    matchLabels:
      app: aiops-mutation-canary
  template:
    metadata:
      labels:
        app: aiops-mutation-canary
    spec:
      containers:
        - name: nginx
          image: nginx:alpine
EOF
run kubectl apply -f "${CANARY_MANIFEST}"
run kubectl -n "${CANARY_NAMESPACE}" rollout status deployment/aiops-mutation-canary --timeout=180s

step 2 "build all images and run preflight gates"
if [[ "${SKIP_BUILD}" != "1" ]]; then
  run env IMAGE_TAG="${RELEASE_TAG}" "${SCRIPT_DIR}/build-images.sh" all
else
  echo "SKIP_IMAGE_BUILD=1: image build skipped"
fi
run helm lint "${CHART_DIR}"
run bash "${SCRIPT_DIR}/verify-aiops-workflow-gates.sh"
run bash "${SCRIPT_DIR}/test-deployment-contracts.sh"

step 3 "bootstrap Helm install"
run helm upgrade --install aiops "${CHART_DIR}" -n "${NAMESPACE}" --create-namespace \
  -f "${CHART_DIR}/values-local-bootstrap.yaml" -f "${SECRET_VALUES}" \
  --set global.imageTag="${RELEASE_TAG}" --set deepflow.enabled=false \
  --wait --timeout 15m

step 4 "wait users-init and schema-migrator"
run kubectl -n "${NAMESPACE}" wait --for=condition=complete job/mysql-users-init --timeout=180s
run kubectl -n "${NAMESPACE}" wait --for=condition=complete job/mysql-init --timeout=300s

step 5 "runtime Helm upgrade"
run helm upgrade aiops "${CHART_DIR}" -n "${NAMESPACE}" \
  -f "${CHART_DIR}/values-local-validation.yaml" -f "${SECRET_VALUES}" \
  --set global.imageTag="${RELEASE_TAG}" --set deepflow.enabled=false \
  --wait --timeout 15m

step 6 "run read-only local stack validator"
run bash "${SCRIPT_DIR}/validate-local-stack.sh"

if [[ "${SKIP_DEEPFLOW}" == "1" ]]; then
  echo "BLOCKED_BY_ENV: DeepFlow was skipped by --skip-deepflow"
else
  step 7 "install DeepFlow (failure is reported as BLOCKED_BY_ENV)"
  if ! helm repo add deepflow https://deepflowio.github.io/deepflow >/dev/null 2>&1; then
    echo "BLOCKED_BY_ENV: DeepFlow Helm repository unavailable"
  elif ! helm upgrade --install deepflow deepflow/deepflow --version 7.1.002 \
      -n "${DEEPFLOW_NAMESPACE}" --create-namespace \
      -f "${CHART_DIR}/values-deepflow.yaml" --wait --timeout 15m; then
    echo "BLOCKED_BY_ENV: DeepFlow did not become Ready"
  else
    step 8 "wire ingest to the installed DeepFlow ClickHouse"
    run helm upgrade aiops "${CHART_DIR}" -n "${NAMESPACE}" --reuse-values \
      --set deepflow.enabled=false \
      --set-string ingest.deepflowChHost="deepflow-clickhouse.deepflow.svc.cluster.local" \
      --set-string ingest.deepflowTenantId="7ed01afc-cc79-4ecd-8767-a2befa6168ad" \
      --wait --timeout 15m
    run kubectl -n "${NAMESPACE}" rollout status deployment/ingest --timeout=240s
  fi
fi

echo "local Fresh Install validation completed for ${RELEASE_TAG}"
