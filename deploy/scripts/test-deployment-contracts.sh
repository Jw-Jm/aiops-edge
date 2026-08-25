#!/usr/bin/env bash
set -euo pipefail

# Fast Helm contract tests for the Fresh Install deployment boundary.
# This script is intentionally independent from a live Kubernetes cluster.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
chart_dir="${repo_root}/deploy/helm/aiops"
tag="git-contract"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-deploy-contracts.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 2
  }
}

require_cmd helm
require_cmd rg

render() {
  local output="$1"
  shift
  helm template aiops "${chart_dir}" \
    --namespace observability \
    -f "${chart_dir}/values-local-validation.yaml" \
    --set global.imageTag="${tag}" \
    --set secrets.jwtSecret="contract-jwt-012345678901234567890123456789" \
    --set secrets.llmEncryptionKey="contract-llm-012345678901234567890123456789" \
    --set secrets.internalToken="contract-internal-012345678901234567890123456789" \
    --set secrets.ingestApiKey="contract-ingest-012345678901234567890123456789" \
    --set secrets.clickhousePassword="contract-clickhouse-012345678901234567890123456789" \
    --set secrets.mysqlRootPassword="contract-root-012345678901234567890123456789" \
    --set secrets.mysqlAppPassword="contract-app-012345678901234567890123456789" \
    --set secrets.mysqlMigratorPassword="contract-migrator-012345678901234567890123456789" \
    --set secrets.llmProxyToken="contract-proxy-token" \
    --set secrets.llmProviderKeys="deepseek:contract-provider-key" \
    --set secrets.orchestratorToQueryToken="contract-o2q-token" \
    --set secrets.orchestratorToQuerySigningKey="contract-o2q-private" \
    --set secrets.orchestratorToQueryVerifyKeys="contract-o2q-public" \
    --set secrets.queryToOrchestratorToken="contract-q2o-token" \
    --set secrets.queryToOrchestratorSigningKey="contract-q2o-private" \
    --set secrets.queryToOrchestratorVerifyKeys="contract-q2o-public" \
    --set secrets.executorToken="contract-executor-token" \
    --set secrets.aiActionExecutorSigningKey="contract-executor-private" \
    --set secrets.aiActionExecutorVerifyKeys="contract-executor-public" \
    --set ipmiExporter.enabled=true \
    "$@" >"${output}"
}

fail_if_contains() {
  local pattern="$1" file="$2" message="$3"
  if rg -n --fixed-strings "${pattern}" "${file}" >/dev/null; then
    echo "contract failed: ${message}" >&2
    rg -n --fixed-strings "${pattern}" "${file}" >&2 || true
    exit 1
  fi
}

require_contains() {
  local pattern="$1" file="$2" message="$3"
  if ! rg -n --fixed-strings "${pattern}" "${file}" >/dev/null; then
    echo "contract failed: ${message}" >&2
    exit 1
  fi
}

echo "[contract] Helm lint"
helm lint "${chart_dir}"

echo "[contract] unified image tag"
render "${tmp_dir}/validation.yaml"
for image in \
  observability-frontend query-api ingest-pipeline ai-orchestrator \
  event-collector ai-action-executor ai-llm-egress-proxy schema-migrator ipmi-exporter
do
  if ! rg -n "image:.*${image}:${tag}" "${tmp_dir}/validation.yaml" >/dev/null; then
    echo "contract failed: ${image} is not rendered with ${tag}" >&2
    exit 1
  fi
done
fail_if_contains ':latest' "${tmp_dir}/validation.yaml" 'self-built images may not use latest'
fail_if_contains 'v1.2.0-p20-24b157a0' "${tmp_dir}/validation.yaml" 'historical fixed image tags remain'
require_contains 'MYSQL_APP_PASSWORD:' "${tmp_dir}/validation.yaml" 'app database password is not rendered'
require_contains 'MYSQL_MIGRATOR_PASSWORD:' "${tmp_dir}/validation.yaml" 'migrator database password is not rendered'

echo "[contract] disabled executor is absent"
render "${tmp_dir}/disabled.yaml" \
  --set aiActionExecutor.enabled=false \
  --set aiActionExecutor.realMutation=false
fail_if_contains 'name: ai-action-executor' "${tmp_dir}/disabled.yaml" 'disabled executor resources are still rendered'

echo "[contract] approved executor is canary scoped"
render "${tmp_dir}/approved.yaml" \
  --set aiActionExecutor.enabled=true \
  --set aiActionExecutor.executionMode=approved \
  --set aiActionExecutor.realMutation=true \
  --set aiActionExecutor.targetNamespaces[0]=aiops-canary
require_contains 'namespace: aiops-canary' "${tmp_dir}/approved.yaml" 'approved executor role is not scoped to canary'
require_contains 'verbs: ["get", "patch"]' "${tmp_dir}/approved.yaml" 'approved executor does not have the exact get/patch verbs'
fail_if_contains 'verbs: ["get", "patch", "delete"]' "${tmp_dir}/approved.yaml" 'executor has delete permission'
fail_if_contains 'namespace: action-test' "${tmp_dir}/approved.yaml" 'historical action-test namespace remains'

echo "deployment contract tests passed"
