#!/usr/bin/env bash
set -euo pipefail

# Register the dedicated kind-aiops-kind-02 target through the public product
# API.  Kubeconfig bytes exist only in a private temporary file and are never
# printed, persisted in the repository, or sent through the browser API.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_JSON="${ROOT_DIR}/tests/manual-e2e/env.json"
CONTEXT="${MANAGED_CLUSTER_CONTEXT:-kind-aiops-kind-02}"
KIND_CLUSTER_NAME="${MANAGED_KIND_CLUSTER_NAME:-aiops-kind-02}"
NAMESPACE="${MANAGED_CLUSTER_SECRET_NAMESPACE:-observability}"
SECRET_NAME="${MANAGED_CLUSTER_SECRET_NAME:-aiops-managed-aiops-kind-02-kubeconfig}"
REACHABLE_HOST="${MANAGED_CLUSTER_API_HOST:-host.docker.internal}"
TLS_SERVER_NAME="${MANAGED_CLUSTER_TLS_SERVER_NAME:-127.0.0.1}"
API_BASE="${API_BASE:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).apiBase' "${ENV_JSON}")}"
USERNAME="${AIOPS_USERNAME:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).username' "${ENV_JSON}")}"
PASSWORD="${AIOPS_PASSWORD:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).password' "${ENV_JSON}")}"
TENANT_ID="${AIOPS_TENANT_ID:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).tenantId' "${ENV_JSON}")}"
CLUSTER_ID="${AIOPS_CLUSTER_ID:-$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1])).clusterId' "${ENV_JSON}")}"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aiops-managed-cluster.XXXXXX")"
COOKIE_FILE="${WORK_DIR}/cookie"
TARGET_KUBECONFIG="${WORK_DIR}/target.kubeconfig"
trap 'rm -rf "${WORK_DIR}"' EXIT

for command in kubectl node curl; do
  command -v "${command}" >/dev/null 2>&1 || { echo "missing command: ${command}" >&2; exit 2; }
done

if ! kubectl config get-contexts -o name | grep -Fxq "${CONTEXT}"; then
  echo "required Kubernetes context is missing: ${CONTEXT}" >&2
  exit 1
fi

# Capture the real kind context, flattening references into the private file.
kubectl config view --raw --flatten --minify --context "${CONTEXT}" > "${TARGET_KUBECONFIG}"
TARGET_CLUSTER="$(kubectl --kubeconfig "${TARGET_KUBECONFIG}" config view -o jsonpath='{.clusters[0].name}')"
[[ -n "${TARGET_CLUSTER}" ]] || { echo 'unable to resolve target cluster name' >&2; exit 1; }
ORIGINAL_SERVER="$(kubectl config view --raw --flatten --minify --context "${CONTEXT}" -o jsonpath='{.clusters[0].cluster.server}')"
REACHABLE_PORT="${MANAGED_CLUSTER_API_PORT:-${ORIGINAL_SERVER##*:}}"
[[ "${REACHABLE_PORT}" =~ ^[0-9]+$ ]] || { echo 'unable to resolve target API port' >&2; exit 1; }

# The host can validate the real kind context through its loopback endpoint,
# while the management Pod reaches the rewritten server address below. Probe
# the target identity before rewriting the private kubeconfig; the public
# registration request performs the second probe from the Query API boundary.
TARGET_UID="$(kubectl --kubeconfig "${TARGET_KUBECONFIG}" get namespace kube-system -o jsonpath='{.metadata.uid}')"
[[ -n "${TARGET_UID}" ]] || { echo 'target kube-system identity probe returned empty' >&2; exit 1; }

# The management cluster reaches the host-published kind API through the
# explicit host name; CA verification remains enabled with the original TLS
# server name. No insecure-skip-tls-verify fallback is allowed.
kubectl --kubeconfig "${TARGET_KUBECONFIG}" config set-cluster "${TARGET_CLUSTER}" \
  --server "https://${REACHABLE_HOST}:${REACHABLE_PORT}" \
  --tls-server-name "${TLS_SERVER_NAME}" >/dev/null

# Create/update only the managed-cluster credential Secret. The command output
# is discarded so the Secret payload cannot appear in CI logs.
kubectl -n "${NAMESPACE}" create secret generic "${SECRET_NAME}" \
  --from-file=kubeconfig="${TARGET_KUBECONFIG}" --dry-run=client -o yaml |
  kubectl apply -f - >/dev/null

AIOPS_USERNAME="${USERNAME}" AIOPS_PASSWORD="${PASSWORD}" node - <<'NODE' > "${WORK_DIR}/login.json"
process.stdout.write(JSON.stringify({ username: process.env.AIOPS_USERNAME, password: process.env.AIOPS_PASSWORD }))
NODE
curl -fsS -c "${COOKIE_FILE}" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/login.json" "${API_BASE}/auth/login" -o "${WORK_DIR}/login-response.json"

AIOPS_TENANT_ID="${TENANT_ID}" AIOPS_CLUSTER_ID="${CLUSTER_ID}" node - <<'NODE' > "${WORK_DIR}/scope.json"
process.stdout.write(JSON.stringify({
  tenant_id: process.env.AIOPS_TENANT_ID,
  cluster_id: process.env.AIOPS_CLUSTER_ID,
}))
NODE
curl -fsS -b "${COOKIE_FILE}" -c "${COOKIE_FILE}" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/scope.json" "${API_BASE}/me/scope" -o "${WORK_DIR}/scope-response.json"

AIOPS_TENANT_ID="${TENANT_ID}" node - <<'NODE' > "${WORK_DIR}/register.json"
const tenant = process.env.AIOPS_TENANT_ID
process.stdout.write(JSON.stringify({
  slug: 'kind-aiops-kind-02', name: 'kind-aiops-kind-02', environment: 'local', region: 'local',
  credential_ref: 'k8s-secret://observability/aiops-managed-aiops-kind-02-kubeconfig',
  type: 'kubernetes', capabilities: 'nodes,namespaces,events', labels: 'managed=true'
}))
NODE
REGISTRATION_STATUS="$(curl -sS -b "${COOKIE_FILE}" -H 'Content-Type: application/json' \
  --data-binary @"${WORK_DIR}/register.json" -o "${WORK_DIR}/registration.json" -w '%{http_code}' "${API_BASE}/clusters")"
if [[ "${REGISTRATION_STATUS}" != "201" && "${REGISTRATION_STATUS}" != "409" ]]; then
  echo "cluster registration failed with HTTP ${REGISTRATION_STATUS}" >&2
  exit 1
fi

curl -fsS -b "${COOKIE_FILE}" "${API_BASE}/clusters" -o "${WORK_DIR}/clusters.json"

node - "${WORK_DIR}/registration.json" "${WORK_DIR}/clusters.json" "${TENANT_ID}" <<'NODE'
const fs = require('fs')
const [file, listFile, tenant] = process.argv.slice(2)
const body = JSON.parse(fs.readFileSync(file, 'utf8'))
if (Object.prototype.hasOwnProperty.call(body, 'kubeconfig') || Object.prototype.hasOwnProperty.call(body.cluster || {}, 'kubeconfig')) {
  throw new Error('registration response leaked kubeconfig material')
}
const list = JSON.parse(fs.readFileSync(listFile, 'utf8'))
const clusters = Array.isArray(list) ? list : (list.clusters || list.data || [])
const registered = clusters.find((item) => item.slug === 'kind-aiops-kind-02' && item.credential_ref === 'k8s-secret://observability/aiops-managed-aiops-kind-02-kubeconfig')
if (!registered || registered.tenant_id !== tenant) throw new Error('tenant-scoped cluster readback failed')
if (body.cluster && body.cluster.credential_ref !== registered.credential_ref) throw new Error('registration response credential binding mismatch')
process.stdout.write(`managed cluster registered: ${registered.cluster_id}\n`)
NODE
