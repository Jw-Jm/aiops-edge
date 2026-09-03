#!/usr/bin/env bash
set -euo pipefail

# Read-only release gate for the converged Investigation workflow. This script
# deliberately never changes EXECUTION_MODE or enables a legacy runtime.
#
# P1-CI1: 支持 AIOPS_GATE_STAGES 分段执行（逗号分隔，默认全部），
# 供 CI 拆分独立 jobs 使用；单项失败不再阻断其他检查的诊断产出。
# 段名: go,workflow-contracts,orchestrator,executor,frontend,helm,contracts
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

STAGES="${AIOPS_GATE_STAGES:-go,orchestrator,executor,frontend,helm,contracts}"
has_stage() { [[ ",${STAGES}," == *",${1},"* ]]; }

go_cache="${GOCACHE:-${TMPDIR:-/tmp}/aiops-gocache}"
export GOCACHE="${go_cache}"

python_bin="${AIOPS_PYTHON:-python3}"
if [[ -x ai-orchestrator/.venv314/bin/python ]]; then
  python_bin="${repo_root}/ai-orchestrator/.venv314/bin/python"
fi

# Host-side workflow tests must never attempt to write the container's
# production path (/var/lib/aiops).  Keep an explicit caller override for CI,
# otherwise isolate all test state in a disposable local directory.
if [[ -z "${AIOPS_DATA_DIR:-}" ]]; then
  AIOPS_DATA_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aiops-workflow-data.XXXXXX")"
  export AIOPS_DATA_DIR
fi

if has_stage go; then
  echo "[G0] Go contract/store/API tests"
  (cd ai-apm-query-go && go test ./... -count=1)
fi

if has_stage orchestrator; then
  echo "[G1-G3] Orchestrator tests"
  (cd ai-orchestrator && "${python_bin}" -m pytest -q)
fi

if has_stage executor; then
  echo "[G5] Action executor tests"
  (cd ai-action-executor && go test ./... -count=1)
fi

if has_stage frontend; then
  echo "[G4] Frontend tests and build"
  (cd observability-frontend && npm run test:run && npm run build)
fi

if has_stage helm; then
  if ! command -v helm >/dev/null 2>&1; then
    echo "helm is required for the release gate" >&2
    exit 1
  fi
  # P1-SUP2: helm lint 用 local 环境（production digest 语义由随后的
  # helm template + sup2 断言校验，lint 只做 chart 结构校验）
  helm lint deploy/helm/aiops --set global.environment=local
  rendered="${TMPDIR:-/tmp}/aiops-workflow-gate-${$}.yaml"
  role="${TMPDIR:-/tmp}/aiops-orchestrator-role-${$}.yaml"
  trap 'rm -f "${rendered}" "${role}"' EXIT
  # Render-only entropy: these values never leave the process and are not
  # suitable for deployment. Production secrets are injected by the release
  # system before helm install.
  # P1-SUP2: production render 必须是 digest 引用。gate 注入同形假 digest，
  # 断言最终渲染没有任何自研镜像使用 mutable tag。
  gate_digest="sha256:0000000000000000000000000000000000000000000000000000000000000000"
  helm template aiops deploy/helm/aiops \
    -f deploy/helm/aiops/values-prod.yaml \
    --set global.imageTag="git-gate123456" \
    --set global.imageDigests.queryApi="${gate_digest}" \
    --set global.imageDigests.ingest="${gate_digest}" \
    --set global.imageDigests.eventCollector="${gate_digest}" \
    --set global.imageDigests.aiOrchestrator="${gate_digest}" \
    --set global.imageDigests.investigationWorker="${gate_digest}" \
    --set global.imageDigests.frontend="${gate_digest}" \
    --set global.imageDigests.aiActionExecutor="${gate_digest}" \
    --set global.imageDigests.credentialBroker="${gate_digest}" \
    --set global.imageDigests.llmEgressProxy="${gate_digest}" \
    --set global.imageDigests.ipmiExporter="${gate_digest}" \
    --set global.imageDigests.clickhouseMigrator="${gate_digest}" \
    --set global.imageDigests.mysqlMigrator="${gate_digest}" \
    --set global.imageDigests.graphSchemaMigrator="${gate_digest}" \
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
    --set 'networkPolicy.kubernetesApiCIDRs={10.0.0.0/8}' \
    --set-string 'internalTLS.clientSAN=query-api.observability.svc.cluster.local\,query-run-dispatch.observability.svc.cluster.local\,ai-orchestrator.observability.svc.cluster.local' \
    >"${rendered}"
  # P1-SUP2: production manifest must reference every self-owned image by
  # digest. Self-owned images are the ones rendered through the
  # aiops.imageWithGlobalTag helper — they all carry @sha256: now.
  echo "[sup2] production self-owned images must be digest-pinned"
  digest_count="$(rg -c '@sha256:' "${rendered}" || true)"
  if [[ "${digest_count}" -lt 13 ]]; then
    echo "production manifest has only ${digest_count} digest references (expected >= 13 self-owned images)" >&2
    exit 1
  fi
  if rg -n 'image: "(query-api|ai-orchestrator|ingest-pipeline|event-collector|observability-frontend|ai-action-executor|ai-credential-broker|ai-llm-egress-proxy|clickhouse-migrator|schema-migrator|graph-schema-migrator|ipmi-exporter|ai-orchestrator):[0-9a-zA-Z]' "${rendered}"; then
    echo "self-owned image still rendered with a mutable tag in the production manifest" >&2
    exit 1
  fi

  awk 'BEGIN { RS="---" } /kind: ClusterRole/ && /name: ai-orchestrator-ops/ { print }' \
    "${rendered}" >"${role}"
  if rg -n 'verbs:.*(patch|create|delete|update)' "${role}"; then
    echo "ai-orchestrator-ops contains mutation verbs" >&2
    exit 1
  fi

  echo "[policy] production safety switches"
  if ! rg -Uq 'name: LEGACY_FLOW_RUNTIME_ENABLED[[:space:]]+value: "0"' "${rendered}" || \
     ! rg -Uq 'name: INVESTIGATOR_ENABLED[[:space:]]+value: "0"' "${rendered}" || \
     ! rg -Uq 'name: LEGACY_DIRECT_MUTATIONS_ENABLED[[:space:]]+value: "0"' "${rendered}"; then
    echo "legacy runtimes or direct mutation routes are enabled in the rendered production manifest" >&2
    exit 1
  fi
fi

if has_stage contracts; then
  echo "[deployment-contracts] Fresh Install Helm/image/Secret/RBAC contracts"
  bash "${repo_root}/deploy/scripts/test-deployment-contracts.sh"
  bash "${repo_root}/deploy/scripts/test-graph-load-contract.sh"
  bash "${repo_root}/deploy/scripts/test-image-digest-contracts.sh"
fi

echo "AIOps workflow gates passed (stages: ${STAGES}; mutation remains disabled)."
