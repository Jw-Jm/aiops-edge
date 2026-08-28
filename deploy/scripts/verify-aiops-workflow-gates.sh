#!/usr/bin/env bash
set -euo pipefail

# Read-only release gate for the converged Investigation workflow. This script
# deliberately never changes EXECUTION_MODE or enables a legacy runtime.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

go_cache="${GOCACHE:-${TMPDIR:-/tmp}/aiops-gocache}"
export GOCACHE="${go_cache}"

python_bin="${AIOPS_PYTHON:-python3}"
if [[ -x ai-orchestrator/.venv314/bin/python ]]; then
  python_bin="${repo_root}/ai-orchestrator/.venv314/bin/python"
fi

echo "[G0] Go contract/store/API tests"
(cd ai-apm-query-go && go test ./... -count=1)

echo "[G0.5] Cross-service durable workflow contract tests"
(cd "${repo_root}" && "${python_bin}" -m pytest tests/workflow-e2e -q)

echo "[G1-G3] Orchestrator tests"
(cd ai-orchestrator && "${python_bin}" -m pytest -q)

echo "[G5] Action executor tests"
(cd ai-action-executor && go test ./... -count=1)

echo "[G4] Frontend tests and build"
(cd observability-frontend && npm run test:run && npm run build)

if command -v helm >/dev/null 2>&1; then
  echo "[G5] Helm render and mutation/RBAC checks"
  helm lint deploy/helm/aiops
  rendered="${TMPDIR:-/tmp}/aiops-workflow-gate-${$}.yaml"
  role="${TMPDIR:-/tmp}/aiops-orchestrator-role-${$}.yaml"
  trap 'rm -f "${rendered}" "${role}"' EXIT
  # Render-only entropy: these values never leave the process and are not
  # suitable for deployment. Production secrets are injected by the release
  # system before helm install.
  helm template aiops deploy/helm/aiops \
    -f deploy/helm/aiops/values-prod.yaml \
    --set global.imageTag="git-gate123456" \
    --set secrets.jwtSecret="gate-jwt-012345678901234567890123456789" \
    --set secrets.llmEncryptionKey="gate-llm-012345678901234567890123456789" \
    --set secrets.internalToken="gate-internal-012345678901234567890123456789" \
    --set secrets.ingestApiKey="gate-ingest-012345678901234567890123456789" \
    --set secrets.clickhousePassword="gate-clickhouse-012345678901234567890123456789" \
    --set secrets.mysqlRootPassword="gate-mysql-012345678901234567890123456789" \
    --set secrets.mysqlAppPassword="gate-app-012345678901234567890123456789" \
    --set secrets.mysqlMigratorPassword="gate-migrator-012345678901234567890123456789" \
    --set secrets.hugeGraphPassword="gate-hugegraph-012345678901234567890123456789" \
    --set secrets.llmProxyToken="gate-proxy-token" \
    --set secrets.llmProviderKeys="deepseek:gate-provider-key" \
    --set secrets.orchestratorToQueryToken="gate-o2q-token" \
    --set secrets.orchestratorToQuerySigningKey="gate-o2q-private" \
    --set secrets.orchestratorToQueryVerifyKeys="gate-o2q-public" \
    --set secrets.queryToOrchestratorToken="gate-q2o-token" \
    --set secrets.queryToOrchestratorSigningKey="gate-q2o-private" \
    --set secrets.queryToOrchestratorVerifyKeys="gate-q2o-public" \
    --set secrets.executorToken="gate-executor-token" \
    --set secrets.aiActionExecutorSigningKey="gate-executor-private" \
    --set secrets.aiActionExecutorVerifyKeys="gate-executor-public" \
    >"${rendered}"
  awk 'BEGIN { RS="---" } /kind: ClusterRole/ && /name: ai-orchestrator-ops/ { print }' \
    "${rendered}" >"${role}"
  if rg -n 'verbs:.*(patch|create|delete|update)' "${role}"; then
    echo "ai-orchestrator-ops contains mutation verbs" >&2
    exit 1
  fi
else
  echo "helm is required for the release gate" >&2
  exit 1
fi

echo "[policy] production safety switches"
if ! rg -Uq 'name: LEGACY_FLOW_RUNTIME_ENABLED[[:space:]]+value: "0"' "${rendered}" || \
   ! rg -Uq 'name: INVESTIGATOR_ENABLED[[:space:]]+value: "0"' "${rendered}" || \
   ! rg -Uq 'name: LEGACY_DIRECT_MUTATIONS_ENABLED[[:space:]]+value: "0"' "${rendered}"; then
  echo "legacy runtimes or direct mutation routes are enabled in the rendered production manifest" >&2
  exit 1
fi

echo "[deployment-contracts] Fresh Install Helm/image/Secret/RBAC contracts"
bash "${repo_root}/deploy/scripts/test-deployment-contracts.sh"
bash "${repo_root}/deploy/scripts/test-graph-load-contract.sh"

echo "AIOps workflow gates passed (mutation remains disabled)."
