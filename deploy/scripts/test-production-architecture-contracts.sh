#!/usr/bin/env bash
set -euo pipefail

# Read-only static/Helm production invariants.  The script deliberately fails
# closed and performs no cluster/network writes.  It accepts an optional chart
# and values path so CI can run the same contract against release candidates.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
chart="${1:-${repo_root}/deploy/helm/aiops}"
values="${2:-${chart}/values-prod.yaml}"
tmp="$(mktemp /tmp/aiops-prod-architecture.XXXXXX.yaml)"
trap 'rm -f "$tmp"' EXIT

helm_secret_args=()
if [[ "${AIOPS_CONTRACT_ALLOW_TEST_SECRETS:-}" == "true" ]]; then
  # Deterministic non-production values for local contract tests only.  The
  # default path intentionally renders values-prod.yaml unchanged and fails on
  # placeholders, so this switch cannot weaken a real deployment gate.
  helm_secret_args=(
    --set-string secrets.jwtSecret=contract-jwt-012345678901234567890123456789
    --set-string secrets.llmEncryptionKey=contract-llm-012345678901234567890123456789
    --set-string secrets.internalToken=contract-internal-012345678901234567890123456789
    --set-string secrets.ingestApiKey=contract-ingest-012345678901234567890123456789
    --set-string secrets.clickhousePassword=contract-clickhouse-012345678901234567890123456789
    --set-string secrets.mysqlRootPassword=contract-root-012345678901234567890123456789
    --set-string secrets.mysqlAppPassword=contract-app-012345678901234567890123456789
    --set-string secrets.mysqlMigratorPassword=contract-migrator-012345678901234567890123456789
    --set-string secrets.llmProxyToken=contract-proxy-token-012345678901234567890123456789
    --set-string secrets.llmProviderKeys=deepseek:contract-provider-key
    --set-string secrets.orchestratorToQueryToken=contract-o2q-token
    --set-string secrets.orchestratorToQuerySigningKey=contract-o2q-private
    --set-string secrets.orchestratorToQueryVerifyKeys=contract-o2q-public
    --set-string secrets.queryToOrchestratorToken=contract-q2o-token
    --set-string secrets.queryToOrchestratorSigningKey=contract-q2o-private
    --set-string secrets.queryToOrchestratorVerifyKeys=contract-q2o-public
    --set-string secrets.executorToken=contract-executor-token
    --set-string secrets.aiActionExecutorSigningKey=contract-executor-private
    --set-string secrets.aiActionExecutorVerifyKeys=contract-executor-public
    --set-string secrets.hugeGraphPassword=contract-hugegraph-password
    --set-string secrets.adminInitialPassword=contract-admin-password
    --set 'networkPolicy.kubernetesApiCIDRs={10.0.0.0/8}'
    --set-string 'internalTLS.clientSAN=query-api.observability.svc.cluster.local\,query-run-dispatch.observability.svc.cluster.local\,ai-orchestrator.observability.svc.cluster.local'
  )
fi

command -v helm >/dev/null || { echo "ARCH-001 missing helm" >&2; exit 2; }
command -v rg >/dev/null || { echo "ARCH-002 missing rg" >&2; exit 2; }

if ((${#helm_secret_args[@]})); then
  helm template aiops "$chart" -f "$values" "${helm_secret_args[@]}" >"$tmp"
else
  # Bash with nounset treats an empty array expansion as an unset variable on
  # some supported versions; keep the default production gate fail-closed.
  helm template aiops "$chart" -f "$values" >"$tmp"
fi

fail() { echo "production architecture contract failed: $1" >&2; exit 1; }
contains() { rg -n --fixed-strings "$1" "$2" >/dev/null; }
forbidden() {
  if contains "$1" "$2"; then
    fail "$3"
  fi
  return 0
}
required() { contains "$1" "$2" || fail "$3"; }

# Browser and deployment identity invariants.
forbidden 'X-Tenant-ID' "$repo_root/observability-frontend/src" 'ARCH-101 browser tenant header';
forbidden 'Header.Get("X-Tenant-ID")' "$repo_root/ai-apm-query-go/internal/api/auth.go" 'ARCH-105 Query API caller-controlled tenant header';
forbidden 'return "default"' "$repo_root/ai-apm-query-go/internal/api/handler.go" 'ARCH-106 Query API implicit tenant default';
forbidden '7ed01afc-cc79-4ecd-8767-a2befa6168ad' "$repo_root/ai-apm-query-go/internal/api/handler.go" 'ARCH-108 hardcoded system tenant fallback';
forbidden 'X-Tenant-ID' "$repo_root/ai-orchestrator/main.py" 'ARCH-107 orchestrator tenant header authorization';
forbidden '7ed01afc-cc79-4ecd-8767-a2befa6168ad' "$repo_root/observability-frontend/src" 'ARCH-102 fixed tenant UUID';
forbidden 'localStorage.getItem('\''token'\'')' "$repo_root/observability-frontend/src" 'ARCH-103 persisted access token';
forbidden 'default: "all"' "$repo_root/observability-frontend/src" 'ARCH-104 default aggregate cluster';

# Single-owner data paths.
forbidden 'INSERT INTO observability' "$repo_root/ai-event-collector" 'ARCH-201 collector direct ClickHouse write';
forbidden 'CLICKHOUSE_PASSWORD' "$repo_root/deploy/helm/aiops/templates/event-collector" 'ARCH-202 collector ClickHouse credential';
forbidden 'POD_SA_ACCESS: "true"' "$tmp" 'ARCH-203 executor long-lived Pod SA';
forbidden 'privileged: true' "$tmp" 'ARCH-204 privileged production workload';
forbidden '/dev/ipmi0' "$tmp" 'ARCH-205 in-band IPMI device mount';
forbidden 'allow-event-collector-to-clickhouse' "$tmp" 'ARCH-206 collector ClickHouse network path';
required 'allow-event-collector-to-ingest' "$tmp" 'ARCH-207 collector unified ingest path missing';
required 'name: INGEST_WAL_DIR' "$tmp" 'ARCH-208 ingest durable acceptance WAL missing';

# Explicit role split and no compatibility default.
required 'name: query-api-http' "$tmp" 'ARCH-301 HTTP role missing';
required 'name: query-run-dispatch' "$tmp" 'ARCH-302 dispatcher role missing';
required 'name: query-alert-eval' "$tmp" 'ARCH-303 evaluator role missing';
forbidden 'role: api' "$tmp" 'ARCH-304 legacy combined API role';
required 'name: default-deny-egress' "$tmp" 'ARCH-305 production egress default deny missing';
required 'name: allow-query-run-dispatch-egress' "$tmp" 'ARCH-306 dispatcher egress allowlist missing';
required 'name: allow-query-alert-eval-egress' "$tmp" 'ARCH-307 evaluator egress allowlist missing';
required 'name: allow-frontend-egress-to-query-api' "$tmp" 'ARCH-308 frontend egress allowlist missing';
required 'name: allow-query-api-to-hugegraph-egress' "$tmp" 'ARCH-309 HugeGraph egress allowlist missing';
required 'name: allow-graph-schema-migrator-egress' "$tmp" 'ARCH-310 graph migrator egress allowlist missing';
required 'name: wait-for-query-api' "$tmp" 'ARCH-312 orchestrator/query-api startup dependency gate missing';
required 'command: ["python", "-m", "mtls_server"]' "$tmp" 'ARCH-313 orchestrator TLS server does not enforce client SAN';
required 'args: ["main:app"' "$tmp" 'ARCH-314 orchestrator TLS server app import is not wired';
required 'args: ["investigation_app:app"' "$tmp" 'ARCH-315 worker TLS server app import is not wired';
required 'PRODUCTION_ROUTE_ALLOWLIST' "$repo_root/ai-orchestrator/production_surface.py" 'ARCH-316 production route allowlist missing';
required '_apply_production_route_surface()' "$repo_root/ai-orchestrator/main.py" 'ARCH-317 production route filter is not wired';
required 'filter_production_routes' "$repo_root/ai-orchestrator/main.py" 'ARCH-318 production route inventory filter missing';
required 'def _default_session_store' "$repo_root/ai-orchestrator/data_cleanup_api.py" 'ARCH-319 cleanup SQLite adapter is not lazy';
forbidden 'from session_store import SessionStore, session_store' "$repo_root/ai-orchestrator/data_cleanup_api.py" 'ARCH-320 cleanup module opens legacy SQLite at import';
required 'name: clickhouse-migrator' "$tmp" 'ARCH-321 ClickHouse migration Job missing';
required '0008_k8s_events_identity_cutover.sql' "$tmp" 'ARCH-322 event identity cutover migration missing';
forbidden 'event_id` String DEFAULT '\''\''' "$repo_root/deploy/helm/aiops/files/clickhouse/init_clickhouse.sql" 'ARCH-323 event_id implicit empty default';
required 'job/clickhouse-migrator' "$repo_root/deploy/scripts/validate-local-stack.sh" 'ARCH-324 ClickHouse migration Job is not part of validation';
required 'event_identity_counts' "$repo_root/deploy/scripts/validate-local-stack.sh" 'ARCH-325 event identity gate is missing';
required '0009_k8s_events_require_identity.sql' "$tmp" 'ARCH-326 event identity default removal migration missing';
required 'event_id_default_kind' "$repo_root/deploy/scripts/validate-local-stack.sh" 'ARCH-327 event_id default removal gate is missing';
required 'tests/' "$repo_root/ai-orchestrator/.dockerignore" 'ARCH-328 production orchestrator image includes test fixtures';
required 'rca_engine_legacy.py' "$repo_root/ai-orchestrator/.dockerignore" 'ARCH-329 production image includes retired RCA implementation';
required 'def _legacy_compat_enabled' "$repo_root/ai-orchestrator/rca_engine/__init__.py" 'ARCH-330 RCA legacy bridge is not explicitly isolated';
required 'def _legacy_graph_snapshot_enabled' "$repo_root/ai-orchestrator/tools.py" 'ARCH-331 production graph snapshot fallback is not explicitly isolated';
required 'if _legacy_public_api_retired():' "$repo_root/ai-orchestrator/main.py" 'ARCH-332 production legacy mutation flags are not fail-closed';
if rg -n 'app: query-api[[:space:]]*$' "$tmp" >/dev/null; then
  fail 'ARCH-311 stale exact query-api selector remains; deployment label is query-api-http'
fi

# LLM egress and placeholder safety.
forbidden 'CHANGE_ME' "$tmp" 'ARCH-401 production placeholder secret';
forbidden 'base_url' "$repo_root/observability-frontend/src/pages" 'ARCH-402 browser provider URL';
required 'AI_LLM_EGRESS_PROXY_URL' "$tmp" 'ARCH-403 LLM egress proxy is not wired';
if ! rg -Uq 'name: LLM_MOCK[[:space:]]+value: "false"' "$tmp"; then
  fail 'ARCH-404 production LLM_MOCK must be explicitly disabled'
fi

# Schema/data contract.
required '`tenant_id` String' "$repo_root/deploy/helm/aiops/files/clickhouse/init_clickhouse.sql" 'ARCH-501 alert tenant column missing';
forbidden 'cluster_id` String DEFAULT '\''default'\''' "$repo_root/deploy/helm/aiops/files/clickhouse/init_clickhouse.sql" 'ARCH-502 default cluster scope';
[[ -f "$repo_root/ai-apm-query-go/internal/store/migrations/versions/0013_session_scope.sql" ]] || fail 'ARCH-503 session scope migration missing';
[[ -f "$repo_root/ai-apm-query-go/internal/store/migrations/versions/0016_ai_chat_turn_id.sql" ]] || fail 'ARCH-504 AICHAT turn idempotency migration missing';
required 'ADD COLUMN turn_id CHAR(36) NULL' "$repo_root/ai-apm-query-go/internal/store/migrations/versions/0016_ai_chat_turn_id.sql" 'ARCH-505 AICHAT turn_id column missing';
required 'uq_ai_chat_message_turn' "$repo_root/ai-apm-query-go/internal/store/migrations/versions/0016_ai_chat_turn_id.sql" 'ARCH-506 AICHAT turn uniqueness constraint missing';

echo "production architecture contracts passed"
