#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_JSON="${ROOT_DIR}/tests/manual-e2e/env.json"
TEST_RUN_ID="${TEST_RUN_ID:-strict-$(date -u +%Y%m%dT%H%M%SZ)}"
ARTIFACT_DIR="${ROOT_DIR}/test-results/${TEST_RUN_ID}"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aiops-strict-preflight.XXXXXX")"
COOKIE_FILE="${WORK_DIR}/cookie"
RELEASE_SHA="$(git -C "${ROOT_DIR}" rev-parse HEAD)"
RELEASE_TAG="${RELEASE_TAG:-git-${RELEASE_SHA:0:12}}"
API_BASE="${API_BASE:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).apiBase' "${ENV_JSON}")}"
USERNAME="${AIOPS_USERNAME:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).username' "${ENV_JSON}")}"
PASSWORD="${AIOPS_PASSWORD:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).password' "${ENV_JSON}")}"
TENANT_ID="${AIOPS_TENANT_ID:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).tenantId' "${ENV_JSON}")}"
CLUSTER_ID="${AIOPS_CLUSTER_ID:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).clusterId' "${ENV_JSON}")}"
MARKER="${STRICT_TELEMETRY_MARKER:-}"
mkdir -p "${ARTIFACT_DIR}"
trap 'rm -rf "${WORK_DIR}"' EXIT

reasons=()
fail() { reasons+=("$1"); }
require_command() { command -v "$1" >/dev/null 2>&1 || fail "missing_command:$1"; }
for command in git node curl kubectl; do require_command "${command}"; done

if [[ -n "$(git -C "${ROOT_DIR}" status --porcelain)" ]]; then
  fail 'git_tree_not_clean'
fi
if [[ ! "${RELEASE_TAG}" =~ ^git-[0-9a-f]{12}$ ]]; then
  fail "invalid_release_tag"
fi

if command -v kubectl >/dev/null 2>&1; then
  kubectl get pods -n observability -o json > "${WORK_DIR}/pods.json" 2>/dev/null || fail 'pods_unavailable'
  if [[ -s "${WORK_DIR}/pods.json" ]]; then
    node - "${WORK_DIR}/pods.json" "${RELEASE_TAG}" <<'NODE' || fail 'pod_readiness_or_image_mismatch'
const fs = require('fs')
const [file, tag] = process.argv.slice(2)
const doc = JSON.parse(fs.readFileSync(file, 'utf8'))
const external = /^(mysql|clickhouse|victoria-|hugegraph|categraf|trace-summary)/
const relevant = (doc.items || []).filter((pod) => !external.test(pod.metadata?.name || '') && pod.status?.phase !== 'Succeeded')
for (const pod of relevant) {
  const ready = (pod.status?.containerStatuses || []).every((c) => c.ready === true)
  const images = (pod.spec?.containers || []).map((c) => c.image || '')
  const tagOK = images.length > 0 && images.every((image) => image.includes(`:${tag}`) || image.includes(`@sha256:`))
  const badReason = (pod.status?.containerStatuses || []).some((c) => /CrashLoopBackOff|OOMKilled/.test(c.state?.waiting?.reason || c.lastState?.terminated?.reason || ''))
  if (!ready || !tagOK || badReason) process.exit(1)
}
NODE
  fi
fi

if [[ -z "${MARKER}" ]]; then
  MARKER="strict-${TEST_RUN_ID}"
fi
login_json="${WORK_DIR}/login.json"
AIOPS_USERNAME="${USERNAME}" AIOPS_PASSWORD="${PASSWORD}" node - <<'NODE' > "${login_json}"
process.stdout.write(JSON.stringify({ username: process.env.AIOPS_USERNAME, password: process.env.AIOPS_PASSWORD }))
NODE
if ! curl -fsS -c "${COOKIE_FILE}" -H 'Content-Type: application/json' --data-binary @"${login_json}" "${API_BASE}/auth/login" -o "${WORK_DIR}/login-response.json"; then
  fail 'product_login_failed'
else
  scope_json="${WORK_DIR}/scope.json"
  AIOPS_TENANT_ID="${TENANT_ID}" AIOPS_CLUSTER_ID="${CLUSTER_ID}" node - <<'NODE' > "${scope_json}"
process.stdout.write(JSON.stringify({ tenant_id: process.env.AIOPS_TENANT_ID, cluster_id: process.env.AIOPS_CLUSTER_ID }))
NODE
  if ! curl -fsS -b "${COOKIE_FILE}" -c "${COOKIE_FILE}" -H 'Content-Type: application/json' \
    --data-binary @"${scope_json}" "${API_BASE}/me/scope" -o "${WORK_DIR}/scope-response.json"; then
    fail 'scope_initialization_failed'
  else
    curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/clusters" -o "${WORK_DIR}/clusters.json" || fail 'cluster_registry_unavailable'
    curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/ai/kg/health" -o "${WORK_DIR}/graph-health.json" || fail 'graph_health_unavailable'
    curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/settings/llm/config" -o "${WORK_DIR}/llm-config.json" || fail 'llm_config_unavailable'
    curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/ai/knowledge/rag/stats" -o "${WORK_DIR}/rag-stats.json" || fail 'knowledge_backend_unavailable'
    curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/traces?hours=24&limit=100" -o "${WORK_DIR}/traces.json" || fail 'trace_query_unavailable'
    curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/logs/query?hours=24&limit=500" -o "${WORK_DIR}/logs.json" || fail 'log_query_unavailable'
    node - "${WORK_DIR}" "${TENANT_ID}" "${CLUSTER_ID}" "${MARKER}" <<'NODE' || fail 'product_preconditions_failed'
const fs = require('fs')
const crypto = require('crypto')
const [dir, tenant, cluster, marker] = process.argv.slice(2)
const read = (name) => JSON.parse(fs.readFileSync(`${dir}/${name}`, 'utf8'))
const clustersBody = read('clusters.json')
const clusters = Array.isArray(clustersBody) ? clustersBody : (clustersBody.clusters || clustersBody.data || [])
if (clusters.filter((c) => c.cluster_id && c.tenant_id === tenant).length < 2) process.exit(1)
if (!clusters.some((c) => c.cluster_id === cluster && c.tenant_id === tenant)) process.exit(1)
if (!JSON.stringify(read('graph-health.json')).match(/healthy|ready|ok/i)) process.exit(1)
const llmConfig = read('llm-config.json')
if (!(llmConfig.configured || llmConfig.data?.configured)) process.exit(1)
if (!JSON.stringify(read('rag-stats.json')).match(/ready|healthy|available|count|total/i)) process.exit(1)
const expectedTraceID = crypto.createHash('sha256').update(`${marker}/1`).digest('hex')
if (!JSON.stringify(read('traces.json')).includes(expectedTraceID) || !JSON.stringify(read('logs.json')).includes(marker)) process.exit(1)
NODE
  fi
fi

if [[ -s "${WORK_DIR}/pods.json" ]]; then
  node - "${WORK_DIR}/pods.json" <<'NODE' || fail 'ai_runtime_pods_missing'
const fs = require('fs')
const doc = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const find = (app) => (doc.items || []).find((pod) => pod.metadata?.labels?.app === app)
const envValue = (pod, name) => pod?.spec?.containers?.flatMap((c) => c.env || []).find((e) => e.name === name)?.value
const gateway = find('ai-orchestrator')
const worker = find('ai-investigation-worker')
const proxy = find('ai-llm-egress-proxy')
const executor = find('ai-action-executor')
if (!gateway || envValue(gateway, 'LLM_MOCK') !== 'false') process.exit(1)
if (!worker || envValue(worker, 'INVESTIGATION_RUNTIME_ENABLED') !== '1' || envValue(worker, 'LLM_MOCK') !== 'false') process.exit(1)
if (!proxy || !executor || envValue(executor, 'EXECUTION_MODE') !== 'approved') process.exit(1)
NODE
fi

kubectl get deployment aiops-action-test -n aiops-action-test >/dev/null 2>&1 || fail 'action_test_target_missing'

status='PASS'
if ((${#reasons[@]} > 0)); then status='BLOCKED'; fi
node - "${ARTIFACT_DIR}/preflight.json" "${TEST_RUN_ID}" "${RELEASE_TAG}" "${MARKER}" "${status}" "${reasons[*]:-}" <<'NODE'
const fs = require('fs')
const [file, runId, tag, marker, status, reasons] = process.argv.slice(2)
fs.writeFileSync(file, `${JSON.stringify({ run_id: runId, release_tag: tag, marker, status, reasons: reasons ? reasons.split(' ') : [], checked_at: new Date().toISOString() }, null, 2)}\n`)
NODE
if [[ "${status}" != PASS ]]; then
  echo "strict manual preflight: BLOCKED; evidence=${ARTIFACT_DIR}/preflight.json" >&2
  exit 1
fi
echo "strict manual preflight: PASS; evidence=${ARTIFACT_DIR}/preflight.json"
