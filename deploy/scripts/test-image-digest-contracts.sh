#!/usr/bin/env bash
set -euo pipefail

# P1-SUP2 Helm contract tests for immutable production image references
# (审核报告 §9.3):
#   1. production missing digest            -> helm template FAIL
#   2. production tag-only identity         -> FAIL
#   3. malformed digest                     -> FAIL
#   4. all self-owned workloads use @sha256 -> PASS (validated here and in
#      verify-aiops-workflow-gates.sh)
#   5. local/dev still allowed global.imageTag
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
chart="${repo_root}/deploy/helm/aiops"
valid="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
bad="sha256:not-a-hex-digest"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

common_secrets=(
  --set secrets.jwtSecret="c-jwt-012345678901234567890123456789"
  --set secrets.llmEncryptionKey="c-llm-012345678901234567890123456789"
  --set secrets.internalToken="c-internal-012345678901234567890123456789"
  --set secrets.ingestApiKey="c-ingest"
  --set secrets.clickhousePassword="c-ch"
  --set secrets.mysqlRootPassword="c-root"
  --set secrets.mysqlAppPassword="c-app"
  --set secrets.mysqlMigratorPassword="c-mig"
  --set secrets.hugeGraphPassword="c-hg"
  --set secrets.llmProxyToken="c-proxy"
  --set secrets.llmProviderKeys="deepseek:c-k"
  --set secrets.orchestratorToQueryToken="c-t"
  --set secrets.orchestratorToQuerySigningKey="c-s"
  --set secrets.orchestratorToQueryVerifyKeys="c-v"
  --set secrets.queryToOrchestratorToken="c-t2"
  --set secrets.queryToOrchestratorSigningKey="c-s2"
  --set secrets.queryToOrchestratorVerifyKeys="c-v2"
  --set secrets.executorToken="c-et"
  --set secrets.aiActionExecutorSigningKey="c-es"
  --set secrets.aiActionExecutorVerifyKeys="c-ev"
  --set 'networkPolicy.kubernetesApiCIDRs={10.0.0.0/8}'
  --set-string 'internalTLS.clientSAN=query-api.observability.svc.cluster.local\,ai-orchestrator.observability.svc.cluster.local'
)

# Test 1+2: production without digests must fail (no silent mutable tag)
echo "[digest-contract] T1/T2: production tag-only render must fail"
if helm template aiops "${chart}" -f "${chart}/values-prod.yaml" "${common_secrets[@]}" \
    --set global.imageTag=v9-tag >"${tmp}/t1.yaml" 2>/dev/null; then
  echo "contract failed: production rendered without digests" >&2
  exit 1
fi

# Test 3: malformed digest must fail
echo "[digest-contract] T3: malformed digest must fail"
if helm template aiops "${chart}" -f "${chart}/values-prod.yaml" "${common_secrets[@]}" \
    --set global.imageDigests.queryApi="${bad}" \
    --set global.imageDigests.ingest="${valid}" \
    --set global.imageDigests.eventCollector="${valid}" \
    --set global.imageDigests.aiOrchestrator="${valid}" \
    --set global.imageDigests.investigationWorker="${valid}" \
    --set global.imageDigests.frontend="${valid}" \
    --set global.imageDigests.aiActionExecutor="${valid}" \
    --set global.imageDigests.credentialBroker="${valid}" \
    --set global.imageDigests.llmEgressProxy="${valid}" \
    --set global.imageDigests.ipmiExporter="${valid}" \
    --set global.imageDigests.clickhouseMigrator="${valid}" \
    --set global.imageDigests.mysqlMigrator="${valid}" \
    --set global.imageDigests.graphSchemaMigrator="${valid}" \
    >"${tmp}/t3.yaml" 2>/dev/null; then
  echo "contract failed: malformed digest was accepted" >&2
  exit 1
fi

# Test 4: full digest render must pass and contain only @sha256 self-owned images
echo "[digest-contract] T4: full digest render"
if ! helm template aiops "${chart}" -f "${chart}/values-prod.yaml" "${common_secrets[@]}" \
    --set global.imageDigests.queryApi="${valid}" \
    --set global.imageDigests.ingest="${valid}" \
    --set global.imageDigests.eventCollector="${valid}" \
    --set global.imageDigests.aiOrchestrator="${valid}" \
    --set global.imageDigests.investigationWorker="${valid}" \
    --set global.imageDigests.frontend="${valid}" \
    --set global.imageDigests.aiActionExecutor="${valid}" \
    --set global.imageDigests.credentialBroker="${valid}" \
    --set global.imageDigests.llmEgressProxy="${valid}" \
    --set global.imageDigests.ipmiExporter="${valid}" \
    --set global.imageDigests.clickhouseMigrator="${valid}" \
    --set global.imageDigests.mysqlMigrator="${valid}" \
    --set global.imageDigests.graphSchemaMigrator="${valid}" \
    >"${tmp}/t4.yaml"; then
  echo "contract failed: full digest render errored" >&2
  exit 1
fi
digest_refs="$(rg -c '@sha256:' "${tmp}/t4.yaml" || true)"
if [[ "${digest_refs}" -lt 9 ]]; then
  echo "contract failed: expected >= 9 digest references, got ${digest_refs}" >&2
  exit 1
fi
# 自研仓库名（render 通过 aiops.imageWithGlobalTag）不得以 mutable tag 形式出现。
self_owned='(ai-orchestrator|query-api|ingest-pipeline|event-collector|observability-frontend|ai-action-executor|ai-credential-broker|ai-llm-egress-proxy|clickhouse-migrator|schema-migrator|graph-schema-migrator|ipmi-exporter)'
if rg -n "image: \"${self_owned}:[0-9a-zA-Z]" "${tmp}/t4.yaml"; then
  echo "contract failed: a self-owned image is rendered with a mutable tag" >&2
  exit 1
fi

# Test 5: local/dev environment still uses global.imageTag
echo "[digest-contract] T5: local render keeps tag mode"
if ! helm template aiops "${chart}" -f "${chart}/values-local-validation.yaml" \
    "${common_secrets[@]}" --set global.imageTag=v9-local-tag >"${tmp}/t5.yaml"; then
  echo "contract failed: local render errored" >&2
  exit 1
fi
if ! rg -q 'image: "query-api:v9-local-tag"' "${tmp}/t5.yaml"; then
  echo "contract failed: local render no longer accepts global.imageTag" >&2
  exit 1
fi

echo "[digest-contract] image digest contracts passed"
