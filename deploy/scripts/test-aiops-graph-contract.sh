#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
chart_dir="${repo_root}/deploy/helm/aiops"
rendered="$(mktemp "${TMPDIR:-/tmp}/aiops-graph-contract.XXXXXX.yaml")"
trap 'rm -f "${rendered}"' EXIT

command -v helm >/dev/null 2>&1 || {
  echo "graph contract failed: helm is required" >&2
  exit 2
}

helm template aiops "${chart_dir}" \
  --namespace observability \
  -f "${chart_dir}/values-local-validation.yaml" \
  --set global.imageTag=git-contract \
  --set secrets.jwtSecret=contract-jwt-012345678901234567890123456789 \
  --set secrets.llmEncryptionKey=contract-llm-012345678901234567890123456789 \
  --set secrets.internalToken=contract-internal-012345678901234567890123456789 \
  --set secrets.ingestApiKey=contract-ingest-012345678901234567890123456789 \
  --set secrets.clickhousePassword=contract-clickhouse-012345678901234567890123456789 \
  --set secrets.mysqlRootPassword=contract-root-012345678901234567890123456789 \
  --set secrets.mysqlAppPassword=contract-app-012345678901234567890123456789 \
  --set secrets.mysqlMigratorPassword=contract-migrator-012345678901234567890123456789 \
  --set secrets.llmProxyToken=contract-proxy-token \
  --set secrets.llmProviderKeys=deepseek:contract-provider-key \
  --set secrets.orchestratorToQueryToken=contract-o2q-token \
  --set secrets.orchestratorToQuerySigningKey=contract-o2q-private \
  --set secrets.orchestratorToQueryVerifyKeys=contract-o2q-public \
  --set secrets.queryToOrchestratorToken=contract-q2o-token \
  --set secrets.queryToOrchestratorSigningKey=contract-q2o-private \
  --set secrets.queryToOrchestratorVerifyKeys=contract-q2o-public \
  --set secrets.executorToken=contract-executor-token \
  --set secrets.aiActionExecutorSigningKey=contract-executor-private \
  --set secrets.aiActionExecutorVerifyKeys=contract-executor-public \
  --set secrets.hugeGraphPassword=contract-hugegraph-password \
  >"${rendered}"

for required in \
  'name: hugegraph-graph-config' \
  'aiops.properties' \
  'store=aiops' \
  'rocksdb.data_path=/var/lib/hugegraph/data/aiops' \
  'rocksdb.wal_path=/var/lib/hugegraph/wal/aiops' \
  'mountPath: /hugegraph-server/conf/graphs/aiops.properties'
do
  rg -n --fixed-strings -- "${required}" "${rendered}" >/dev/null || {
    echo "graph contract failed: missing ${required}" >&2
    exit 1
  }
done

echo "aiops HugeGraph graph contract passed"
