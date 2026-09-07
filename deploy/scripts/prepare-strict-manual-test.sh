#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_JSON="$ROOT_DIR/tests/manual-e2e/env.json"
TEST_RUN_ID="${TEST_RUN_ID:-strict-$(date -u +%Y%m%dT%H%M%SZ)}"
MARKER="strict-${TEST_RUN_ID}"
ARTIFACT_DIR="$ROOT_DIR/test-results/$TEST_RUN_ID"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aiops-strict-telemetry.XXXXXX")"
COOKIE_FILE="$WORK_DIR/cookie"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$ARTIFACT_DIR"

BASE_URL="$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).baseURL' "$ENV_JSON")"
API_BASE="$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).apiBase' "$ENV_JSON")"
TENANT_ID="$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).tenantId' "$ENV_JSON")"
CLUSTER_ID="$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).clusterId' "$ENV_JSON")"
USERNAME="$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).username' "$ENV_JSON")"
PASSWORD="$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).password' "$ENV_JSON")"
INGEST_URL="https://ingest.observability.svc.cluster.local:8080"

login_payload="$(node -e 'process.stdout.write(JSON.stringify({username: process.argv[1], password: process.argv[2]}))' "$USERNAME" "$PASSWORD")"
curl -fsS -c "$COOKIE_FILE" -H 'Content-Type: application/json' -d "$login_payload" "$API_BASE/auth/login" -o "$WORK_DIR/login.json"
scope_payload="$(node -e 'process.stdout.write(JSON.stringify({tenant_id: process.argv[1], cluster_id: process.argv[2]}))' "$TENANT_ID" "$CLUSTER_ID")"
curl -fsS -b "$COOKIE_FILE" -c "$COOKIE_FILE" -H 'Content-Type: application/json' -d "$scope_payload" "$API_BASE/me/scope" -o "$WORK_DIR/scope.json"

curl -fsS -b "$COOKIE_FILE" "$API_BASE/dashboard/stats" -o "$WORK_DIR/initial-stats.json"
curl -fsS -b "$COOKIE_FILE" "$API_BASE/traces?hours=24&limit=20" -o "$WORK_DIR/initial-traces.json"

INGEST_POD="$(kubectl get pods -n observability -l app=ingest -o jsonpath='{.items[0].metadata.name}')"
if [[ -z "$INGEST_POD" ]]; then
  echo 'strict telemetry preparation: ingest Pod not found' >&2
  exit 1
fi

# The Pod already receives INGEST_API_KEY and AIOPS_TLS_* from Kubernetes Secrets.
# No secret is copied to the shell command line or printed in output.
kubectl exec -n observability "$INGEST_POD" -c ingest -- env LOADGEN_MARKER="$MARKER" /loadgen \
  --strict --once --rounds 1 --marker "$MARKER" --ingest "$INGEST_URL" \
  --tls-server-name ingest.observability.svc.cluster.local

ready=0
TIMEOUT_SECONDS=120
POLL_SECONDS=2
MAX_ATTEMPTS=$((TIMEOUT_SECONDS / POLL_SECONDS))
for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
  curl -fsS -b "$COOKIE_FILE" "$API_BASE/dashboard/stats" -o "$WORK_DIR/stats.json"
  curl -fsS -b "$COOKIE_FILE" "$API_BASE/services?hours=24&limit=200" -o "$WORK_DIR/services.json"
  curl -fsS -b "$COOKIE_FILE" "$API_BASE/traces?service=payments&hours=24&limit=50" -o "$WORK_DIR/traces.json"
  curl -fsS -b "$COOKIE_FILE" "$API_BASE/logs/query?service_name=payments&hours=24&limit=50" -o "$WORK_DIR/logs.json"
  curl -fsS -b "$COOKIE_FILE" "$API_BASE/services/map?hours=24" -o "$WORK_DIR/service-map.json"

  if node - "$WORK_DIR" "$MARKER" <<'NODE'
const fs = require('fs')
const dir = process.argv[2]
const marker = process.argv[3]
const read = (name) => JSON.parse(fs.readFileSync(`${dir}/${name}`, 'utf8'))
const stats = read('stats.json')
const services = JSON.stringify(read('services.json'))
const traces = JSON.stringify(read('traces.json'))
const logs = JSON.stringify(read('logs.json'))
const map = JSON.stringify(read('service-map.json'))
const trend = Array.isArray(stats.trend) && stats.trend.length > 0
const servicesReady = services.includes('payments') && services.includes('orders')
const tracesReady = traces.includes(marker) || traces.includes('payments')
const logsReady = logs.includes(marker)
const topologyReady = /payments.*orders|orders.*payments/.test(map)
process.exit(trend && servicesReady && tracesReady && logsReady && topologyReady ? 0 : 1)
NODE
  then
    ready=1
    break
  fi
  sleep "$POLL_SECONDS"
done

node - "$ARTIFACT_DIR/telemetry-preparation.json" "$TEST_RUN_ID" "$MARKER" "$ready" <<'NODE'
const fs = require('fs')
const [file, runId, marker, ready] = process.argv.slice(2)
const evidence = {
  run_id: runId,
  marker,
  telemetry_ready: ready === '1',
  checked_at: new Date().toISOString(),
  source: 'production_otlp_ingest',
}
fs.writeFileSync(file, `${JSON.stringify(evidence, null, 2)}\n`)
NODE

if [[ "$ready" != 1 ]]; then
  echo "strict telemetry preparation: timeout; evidence=$ARTIFACT_DIR/telemetry-preparation.json" >&2
  exit 1
fi
echo "strict telemetry preparation: PASS; evidence=$ARTIFACT_DIR/telemetry-preparation.json"
