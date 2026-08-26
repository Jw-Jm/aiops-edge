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

fail_if_matches() {
  local pattern="$1" file="$2" message="$3"
  if rg -n "${pattern}" "${file}" >/dev/null; then
    echo "contract failed: ${message}" >&2
    rg -n "${pattern}" "${file}" >&2 || true
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
require_contains 'MYSQL_DATABASE' "${tmp_dir}/validation.yaml" 'MySQL database name is not configured for a fresh data directory'
require_contains 'CREATE DATABASE IF NOT EXISTS aiops' "${tmp_dir}/validation.yaml" 'users-init does not create the application database'
require_contains 'DEEPFLOW_ENABLED' "${tmp_dir}/validation.yaml" 'frontend does not receive the optional DeepFlow switch'
require_contains 'CREATE TABLE IF NOT EXISTS observability.k8s_events' "${tmp_dir}/validation.yaml" 'ClickHouse bootstrap omits the event-collector table'
require_contains 'name: CLICKHOUSE_HTTP_URL' "${tmp_dir}/validation.yaml" 'ingest does not configure the ClickHouse Trace SoT HTTP endpoint'
require_contains 'name: TRACE_SOT_MODE' "${tmp_dir}/validation.yaml" 'ingest does not enforce fail-closed Trace SoT mode'

echo "[contract] disabled executor is absent"
render "${tmp_dir}/disabled.yaml" \
  --set aiActionExecutor.enabled=false \
  --set aiActionExecutor.realMutation=false
fail_if_contains 'name: ai-action-executor' "${tmp_dir}/disabled.yaml" 'disabled executor resources are still rendered'

echo "[contract] bootstrap contains only stateful resources"
helm template aiops "${chart_dir}" \
  --namespace observability \
  -f "${chart_dir}/values-local-bootstrap.yaml" \
  --set global.imageTag="${tag}" \
  --set secrets.jwtSecret="contract-jwt-012345678901234567890123456789" \
  --set secrets.llmEncryptionKey="contract-llm-012345678901234567890123456789" \
  --set secrets.internalToken="contract-internal-012345678901234567890123456789" \
  --set secrets.ingestApiKey="contract-ingest-012345678901234567890123456789" \
  --set secrets.clickhousePassword="contract-clickhouse-012345678901234567890123456789" \
  --set secrets.mysqlRootPassword="contract-root-012345678901234567890123456789" \
  --set secrets.mysqlAppPassword="contract-app-012345678901234567890123456789" \
  --set secrets.mysqlMigratorPassword="contract-migrator-012345678901234567890123456789" \
  >"${tmp_dir}/bootstrap.yaml"
for runtime_resource in \
  '^  name: query-api$' \
  '^  name: ai-investigation-worker$' \
  '^  name: ai-llm-egress-proxy$' \
  '^  name: ingest$' \
  '^  name: frontend$' \
  '^  name: ai-action-executor$'
do
  fail_if_matches "${runtime_resource}" "${tmp_dir}/bootstrap.yaml" "bootstrap renders runtime resource ${runtime_resource}"
done
fail_if_matches '^  name: backup-pvc$' "${tmp_dir}/bootstrap.yaml" 'bootstrap renders a backup PVC that can block Helm wait'

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
