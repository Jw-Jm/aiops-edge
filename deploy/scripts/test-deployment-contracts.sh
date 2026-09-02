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

if [[ ! -x "${repo_root}/deploy/scripts/import-local-secrets-from-k8s.sh" ]]; then
  echo "contract failed: Kubernetes Secret importer is missing or not executable" >&2
  exit 1
fi

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
    --set secrets.hugeGraphPassword="contract-hugegraph-password" \
    --set ipmiExporter.enabled=true \
    "$@" >"${output}"
}

fail_if_contains() {
  local pattern="$1" file="$2" message="$3"
  if rg -n --fixed-strings -- "${pattern}" "${file}" >/dev/null; then
    echo "contract failed: ${message}" >&2
    rg -n --fixed-strings -- "${pattern}" "${file}" >&2 || true
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

fail_if_multiline_matches() {
  local pattern="$1" file="$2" message="$3"
  if rg -n -U "${pattern}" "${file}" >/dev/null; then
    echo "contract failed: ${message}" >&2
    rg -n -U "${pattern}" "${file}" >&2 || true
    exit 1
  fi
}

require_contains() {
  local pattern="$1" file="$2" message="$3"
  if ! rg -n --fixed-strings -- "${pattern}" "${file}" >/dev/null; then
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
  event-collector ai-action-executor ai-llm-egress-proxy schema-migrator graph-schema-migrator clickhouse-migrator ipmi-exporter
do
  if ! rg -n "image:.*${image}:${tag}" "${tmp_dir}/validation.yaml" >/dev/null; then
    echo "contract failed: ${image} is not rendered with ${tag}" >&2
    exit 1
  fi
done
# A `helm upgrade --reuse-values` retains historical component-level image
# values.  They are accepted as registry/repository overrides, but the
# release tag must still come exclusively from global.imageTag.  Render with
# deliberately stale values to prevent mixed-version deployments from
# regressing silently.
render "${tmp_dir}/stale-component-images.yaml" \
  --set queryApi.image="query-api:git-stale" \
  --set investigationWorker.image.repository="ai-orchestrator" \
  --set investigationWorker.image.tag="git-stale" \
  --set aiOrchestrator.image="ai-orchestrator:git-stale" \
  --set frontend.image="observability-frontend:git-stale" \
  --set ingest.image="ingest-pipeline:git-stale" \
  --set eventCollector.image="event-collector:git-stale" \
  --set aiActionExecutor.image.tag="git-stale" \
  --set credentialBroker.image.tag="git-stale" \
  --set llmEgressProxy.image.tag="git-stale" \
  --set mysql.migratorImage="schema-migrator:git-stale" \
  --set clickhouse.migratorImage="clickhouse-migrator:git-stale" \
  --set hugeGraph.schemaMigratorImage="graph-schema-migrator:git-stale" \
  --set ipmiExporter.image="ipmi-exporter:git-stale"
fail_if_contains 'git-stale' "${tmp_dir}/stale-component-images.yaml" \
  'historical component image tags override global.imageTag'
for image in \
  observability-frontend query-api ingest-pipeline ai-orchestrator \
  event-collector ai-action-executor ai-llm-egress-proxy schema-migrator \
  graph-schema-migrator clickhouse-migrator ipmi-exporter
do
  if ! rg -n "image:.*${image}:${tag}" "${tmp_dir}/stale-component-images.yaml" >/dev/null; then
    echo "contract failed: stale override did not converge ${image} to ${tag}" >&2
    exit 1
  fi
done
fail_if_contains ':latest' "${tmp_dir}/validation.yaml" 'self-built images may not use latest'
fail_if_contains 'v1.2.0-p20-24b157a0' "${tmp_dir}/validation.yaml" 'historical fixed image tags remain'
require_contains 'MYSQL_APP_PASSWORD:' "${tmp_dir}/validation.yaml" 'app database password is not rendered'
require_contains 'MYSQL_MIGRATOR_PASSWORD:' "${tmp_dir}/validation.yaml" 'migrator database password is not rendered'
require_contains 'name: hugegraph' "${tmp_dir}/validation.yaml" 'HugeGraph StatefulSet is not rendered'
require_contains 'hugegraph/hugegraph:1.7.0' "${tmp_dir}/validation.yaml" 'HugeGraph version is not pinned to 1.7.0'
require_contains 'name: graph-schema-migrator' "${tmp_dir}/validation.yaml" 'graph schema migrator Job is not rendered'
require_contains 'mountPath: /var/lib/hugegraph' "${tmp_dir}/validation.yaml" 'HugeGraph PVC must mount the documented data root'
fail_if_contains 'mountPath: /var/lib/hugegraph/data' "${tmp_dir}/validation.yaml" 'HugeGraph PVC is mounted below the documented data root'
require_contains 'rocksdb.data_path=/var/lib/hugegraph/data' "${tmp_dir}/validation.yaml" 'HugeGraph RocksDB data path is not redirected to the PVC'
require_contains 'rocksdb.wal_path=/var/lib/hugegraph/wal' "${tmp_dir}/validation.yaml" 'HugeGraph RocksDB WAL path is not redirected to the PVC'
require_contains 'name: hugegraph-graph-config' "${tmp_dir}/validation.yaml" 'named HugeGraph graph ConfigMap is not rendered'
require_contains 'aiops.properties' "${tmp_dir}/validation.yaml" 'named HugeGraph graph configuration is not rendered'
require_contains 'store=aiops' "${tmp_dir}/validation.yaml" 'named HugeGraph graph store is not isolated'
require_contains 'rocksdb.data_path=/var/lib/hugegraph/data/aiops' "${tmp_dir}/validation.yaml" 'named HugeGraph graph data path is not isolated'
require_contains 'rocksdb.wal_path=/var/lib/hugegraph/wal/aiops' "${tmp_dir}/validation.yaml" 'named HugeGraph graph WAL path is not isolated'
require_contains 'mountPath: /hugegraph-server/conf/graphs/aiops.properties' "${tmp_dir}/validation.yaml" 'named HugeGraph graph configuration is not mounted into the server'
require_contains 'name: PASSWORD' "${tmp_dir}/validation.yaml" 'HugeGraph auth password is not wired into the server'
require_contains 'curl -fsS -u' "${tmp_dir}/validation.yaml" 'HugeGraph probes do not authenticate against the server'
require_contains 'AUTH="$(printf' "${tmp_dir}/validation.yaml" 'graph schema migrator wait init container does not build a HugeGraph Basic Auth header'
require_contains 'wget -q --header="Authorization: Basic ${AUTH}"' "${tmp_dir}/validation.yaml" 'graph schema migrator wait init container does not authenticate against HugeGraph'
require_contains 'GRAPH_BACKEND' "${tmp_dir}/validation.yaml" 'query-api graph backend is not configured'
require_contains 'name: AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE' "${tmp_dir}/validation.yaml" 'first-login password policy is not wired'
require_contains 'value: "false"' "${tmp_dir}/validation.yaml" 'local validation must temporarily disable first-login password change'
require_contains 'HUGEGRAPH_URL' "${tmp_dir}/validation.yaml" 'query-api HugeGraph URL is not configured'
require_contains 'name: AIOPS_TLS_CLIENT_SAN' "${tmp_dir}/validation.yaml" 'mTLS client SAN allowlist is not wired'
require_contains 'MYSQL_DATABASE' "${tmp_dir}/validation.yaml" 'MySQL database name is not configured for a fresh data directory'
require_contains 'CREATE DATABASE IF NOT EXISTS aiops' "${tmp_dir}/validation.yaml" 'users-init does not create the application database'
require_contains 'DEEPFLOW_ENABLED' "${tmp_dir}/validation.yaml" 'frontend does not receive the optional DeepFlow switch'
require_contains 'AIOPS_INTERNAL_TLS_ENABLED' "${tmp_dir}/validation.yaml" 'frontend does not receive the internal TLS transport switch'
require_contains 'name: internal-tls-ca' "${tmp_dir}/validation.yaml" 'frontend/metrics scraper do not mount the internal CA in TLS mode'
require_contains 'scheme: https' "${tmp_dir}/validation.yaml" 'internal TLS scrape jobs do not switch to HTTPS'
require_contains 'ca_file: /etc/vm/tls/ca.crt' "${tmp_dir}/validation.yaml" 'internal TLS scrape jobs do not verify the platform CA'
require_contains 'cert_file: /etc/vm/tls/tls.crt' "${tmp_dir}/validation.yaml" 'internal TLS scrape jobs do not present a client certificate'
require_contains 'key_file: /etc/vm/tls/tls.key' "${tmp_dir}/validation.yaml" 'internal TLS scrape jobs do not present a client key'
require_contains 'server_name: ingest.observability.svc.cluster.local' "${tmp_dir}/validation.yaml" 'ingest scrape job does not use the certificate SAN'
require_contains 'server_name: ai-orchestrator.observability.svc.cluster.local' "${tmp_dir}/validation.yaml" 'orchestrator scrape job does not use the certificate SAN'
require_contains 'server_name: query-api.observability.svc.cluster.local' "${tmp_dir}/validation.yaml" 'query-api scrape job does not use the certificate SAN'
require_contains 'proxy_ssl_verify on' "${repo_root}/observability-frontend/nginx.conf" 'frontend HTTPS upstream verification is not configured'
require_contains 'AIOPS_QUERY_TLS_BEGIN' "${repo_root}/observability-frontend/docker-entrypoint.sh" 'frontend TLS transport switch is not wired'
require_contains 'CREATE TABLE IF NOT EXISTS observability.k8s_events' "${tmp_dir}/validation.yaml" 'ClickHouse bootstrap omits the event-collector table'
require_contains 'name: CLICKHOUSE_HTTP_URL' "${tmp_dir}/validation.yaml" 'ingest does not configure the ClickHouse Trace SoT HTTP endpoint'
require_contains 'name: TRACE_SOT_MODE' "${tmp_dir}/validation.yaml" 'ingest does not enforce fail-closed Trace SoT mode'
require_contains 'CREATE TABLE IF NOT EXISTS observability.trace_summary_state' "${tmp_dir}/validation.yaml" 'ClickHouse bootstrap omits the Trace Summary table'
require_contains 'name: clickhouse-migrator' "${tmp_dir}/validation.yaml" 'ClickHouse migration Job is not rendered'
require_contains '--password=\"$CH_PROBE_PASSWORD\"' "${tmp_dir}/validation.yaml" 'ClickHouse probes must pass passwords as a single option argument'
fail_if_contains '--password \"$CH_PROBE_PASSWORD\"' "${tmp_dir}/validation.yaml" 'ClickHouse probes split a password argument that may begin with a dash'
require_contains '0008_k8s_events_identity_cutover.sql' "${tmp_dir}/validation.yaml" 'ClickHouse identity cutover migration is not mounted'
require_contains '0009_k8s_events_require_identity.sql' "${tmp_dir}/validation.yaml" 'ClickHouse event identity enforcement migration is not mounted'
require_contains 'ENGINE = SummingMergeTree' "${tmp_dir}/validation.yaml" 'service_topology must aggregate repeated ingest flushes without replacement loss'
require_contains '0010_service_topology_summing.sql' "${tmp_dir}/validation.yaml" 'service_topology summing migration is not mounted'
require_contains 'event_id` String' "${tmp_dir}/validation.yaml" 'ClickHouse event_id must be required without a default'
require_contains 'ENGINE = AggregatingMergeTree' "${tmp_dir}/validation.yaml" 'Trace Summary table is not a pre-aggregated ClickHouse table'
require_contains 'CREATE MATERIALIZED VIEW IF NOT EXISTS observability.trace_spans_to_summary_state' "${tmp_dir}/validation.yaml" 'Trace Summary incremental builder is missing'
require_contains 'CREATE TABLE IF NOT EXISTS observability.trace_summary_index' "${tmp_dir}/validation.yaml" 'Trace Summary candidate index is missing'
require_contains 'CREATE MATERIALIZED VIEW IF NOT EXISTS observability.trace_spans_to_summary_index' "${tmp_dir}/validation.yaml" 'Trace Summary candidate index builder is missing'
fail_if_contains 'trace_spans_to_summary_index_v2' "${tmp_dir}/validation.yaml" 'legacy duplicate Trace Summary index builder remains'
fail_if_contains 'trace_summary_index_v2_backfill_markers' "${tmp_dir}/validation.yaml" 'legacy duplicate Trace Summary index marker remains'
require_contains 'toStartOfFiveMinutes(start_time)' "${tmp_dir}/validation.yaml" 'Trace Summary candidate index has no bounded time bucket'
require_contains 'latest_start_key' "${tmp_dir}/validation.yaml" 'Trace Summary candidate index has no newest-first physical key'
require_contains 'arrayStringConcat(groupUniqArray' "${tmp_dir}/validation.yaml" 'Trace Summary candidate index does not retain bounded operation/url search text'
deepflow_values="${chart_dir}/values-deepflow.yaml"
require_contains 'trigger_threshold: 0' "${deepflow_values}" 'DeepFlow Agent system breaker is not explicitly disabled'
require_contains 'recovery_threshold: 0' "${deepflow_values}" 'DeepFlow Agent breaker recovery threshold is not explicitly disabled'
require_contains 'percentage_trigger_threshold: 0' "${deepflow_values}" 'DeepFlow Agent disk breaker is not explicitly disabled'
deepflow_render_contract="${repo_root}/deploy/scripts/test-deepflow-otlp-render.sh"
deepflow_cutover_harness="${repo_root}/deploy/scripts/verify-deepflow-otlp-cutover.sh"
if [[ ! -x "${deepflow_render_contract}" ]]; then
  echo "contract failed: DeepFlow OTLP render contract is missing or not executable" >&2
  exit 1
fi
if [[ ! -x "${deepflow_cutover_harness}" ]]; then
  echo "contract failed: DeepFlow OTLP cutover evidence harness is missing or not executable" >&2
  exit 1
fi
require_contains 'protocol: opentelemetry' "${deepflow_values}" 'DeepFlow OTLP exporter protocol is not configured'
require_contains 'flow_log.l7_flow_log' "${deepflow_values}" 'DeepFlow OTLP exporter source is not configured'
require_contains 'ingest.observability.svc.cluster.local:4317' "${deepflow_values}" 'DeepFlow OTLP exporter endpoint is not canonical'
require_contains 'BLOCKED_BY_ENV' "${deepflow_cutover_harness}" 'DeepFlow cutover harness does not fail closed on missing live evidence'
require_contains 'uniqExactState' "${tmp_dir}/validation.yaml" 'Trace Summary does not deduplicate spans by stable identity'
require_contains 'span_dedup_key' "${tmp_dir}/validation.yaml" 'Trace Summary does not have a stable span identity'
require_contains 'name: trace-summary-backfill-' "${tmp_dir}/validation.yaml" 'Trace Summary history backfill Job is not rendered'
require_contains 'name: trace-summary-backfill-' "${tmp_dir}/validation.yaml" 'Trace Summary backfill ConfigMap is not rendered'
verify_graph_script="${repo_root}/deploy/scripts/verify-kubernetes-graph.sh"
if [[ ! -x "${verify_graph_script}" ]]; then
  echo "contract failed: Kubernetes graph verification script is missing or not executable" >&2
  exit 1
fi
require_contains 'graphspaces/${graphspace}/graphs/${graph}' "${verify_graph_script}" 'Kubernetes graph verification does not inspect the configured named graph'
require_contains 'query_api_deployment="${QUERY_API_DEPLOYMENT:-query-api-http}"' "${verify_graph_script}" 'Kubernetes graph verification does not target the actual Query API deployment'
require_contains 'deploy/${query_api_deployment}' "${verify_graph_script}" 'Kubernetes graph verification does not exec the configured Query API deployment'
require_contains 'source=kubernetes status=success' "${verify_graph_script}" 'Kubernetes graph verification does not require a successful source reconcile'
require_contains 'graph/vertices' "${verify_graph_script}" 'Kubernetes graph verification does not inspect a projected entity'
if ! rg -n --fixed-strings 'finalizeAggregation' "${repo_root}/ai-apm-query-go/internal/query/traces.go" >/dev/null; then
  echo "contract failed: Trace list query does not finalize Summary aggregate states" >&2
  exit 1
fi
require_contains 'max_block_size=256' "${repo_root}/ai-apm-query-go/internal/query/traces.go" 'Trace candidate index query has no bounded read block'
backfill_script="${repo_root}/deploy/scripts/backfill-trace-summaries.sh"
if [[ ! -x "${backfill_script}" ]]; then
  echo "contract failed: Trace Summary backfill script is missing or not executable" >&2
  exit 1
fi
require_contains 'INSERT INTO observability.trace_summary_state' "${backfill_script}" 'Trace Summary backfill target is missing'
require_contains 'FROM observability.trace_spans' "${backfill_script}" 'Trace Summary backfill source is missing'
require_contains 'WHERE date=toDate' "${backfill_script}" 'Trace Summary backfill is not date-partitioned'
require_contains 'GROUP BY tenant_id, cluster_id, date, trace_id' "${backfill_script}" 'Trace Summary backfill does not aggregate one date partition at a time'
require_contains 'GROUP BY tenant_id, cluster_id, date, time_bucket, trace_id, service_name' "${backfill_script}" 'Trace Summary index backfill is not one row per trace/service/time bucket'
fail_if_contains 'index_v2' "${backfill_script}" 'legacy duplicate Trace Summary index backfill remains'
require_contains 'max_bytes_before_external_group_by' "${backfill_script}" 'Trace Summary backfill lacks bounded aggregation settings'
require_contains '</dev/null' "${backfill_script}" 'Trace Summary backfill leaks its date stream into INSERT stdin'
rebuild_script="${repo_root}/deploy/scripts/rebuild-trace-summary-index.sh"
if [[ ! -x "${rebuild_script}" ]]; then
  echo "contract failed: Trace Summary index convergence script is missing or not executable" >&2
  exit 1
fi
require_contains 'REBUILD_LEGACY_INDEX=true' "${rebuild_script}" 'derived Trace Summary index rebuild is not explicit'
require_contains 'TRUNCATE TABLE observability.trace_summary_index' "${rebuild_script}" 'Trace Summary index convergence does not rebuild the derived index'
require_contains 'trace_spans_to_summary_index_v2' "${rebuild_script}" 'Trace Summary convergence does not remove the legacy duplicate MV'
probe_timeout_count="$(rg -c --fixed-strings 'timeoutSeconds: 5' "${tmp_dir}/validation.yaml" || true)"
if [[ "${probe_timeout_count}" -lt 2 ]]; then
  echo "contract failed: ClickHouse readiness/liveness probes need a five-second timeout" >&2
  exit 1
fi
probe_failure_count="$(rg -c --fixed-strings 'failureThreshold: 6' "${tmp_dir}/validation.yaml" || true)"
if [[ "${probe_failure_count}" -lt 2 ]]; then
  echo "contract failed: ClickHouse readiness/liveness probes need a six-failure threshold" >&2
  exit 1
fi

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
  --set secrets.hugeGraphPassword="contract-hugegraph-password" \
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
require_contains 'resources: ["pods"]' "${tmp_dir}/approved.yaml" 'approved executor does not have pod action resource rules'
require_contains 'resources: ["pods/eviction"]' "${tmp_dir}/approved.yaml" 'approved executor does not have pod eviction resource rules'
require_contains 'name: ai-action-executor-node' "${tmp_dir}/approved.yaml" 'approved executor does not have node action boundary'
fail_if_multiline_matches 'resources: \["deployments", "deployments/scale", "statefulsets", "statefulsets/scale", "daemonsets"\][[:space:]]*\n[[:space:]]*verbs: \[[^]]*"delete"' "${tmp_dir}/approved.yaml" 'executor apps rule unexpectedly includes delete'
fail_if_contains 'namespace: action-test' "${tmp_dir}/approved.yaml" 'historical action-test namespace remains'

echo "deployment contract tests passed"
