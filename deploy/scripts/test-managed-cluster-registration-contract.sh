#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REGISTER="${ROOT_DIR}/deploy/scripts/register-local-managed-cluster.sh"
RBAC="${ROOT_DIR}/deploy/helm/aiops/templates/query-api/rbac.yaml"
VALUES="${ROOT_DIR}/deploy/helm/aiops/values-local-validation.yaml"

[[ -x "${REGISTER}" ]] || { echo 'managed cluster registration script must be executable' >&2; exit 1; }
grep -Fq 'kind-aiops-kind-02' "${REGISTER}" || { echo 'script must pin the dedicated kind context' >&2; exit 1; }
grep -Fq 'credential_ref' "${REGISTER}" || { echo 'script must register through credential_ref' >&2; exit 1; }
grep -Fq 'kube-system' "${REGISTER}" || { echo 'script must probe target identity before Secret creation' >&2; exit 1; }
grep -Fq 'tls-server-name' "${REGISTER}" || { echo 'script must preserve explicit TLS server-name verification' >&2; exit 1; }
grep -Fq 'aiops-managed-aiops-kind-02-kubeconfig' "${REGISTER}" || { echo 'script must use the dedicated Secret name' >&2; exit 1; }
grep -Fq 'credentialSecretNames' "${RBAC}" || { echo 'RBAC must support an explicit Secret allow-list' >&2; exit 1; }
grep -Fq 'aiops-managed-aiops-kind-02-kubeconfig' "${VALUES}" || { echo 'local profile must allow the managed cluster Secret' >&2; exit 1; }
grep -Fq 'TARGET_KUBECONFIG=' "${REGISTER}" || { echo 'kubeconfig must be held in a private temporary file' >&2; exit 1; }
if grep -nE 'echo .*TARGET_KUBECONFIG|printf .*TARGET_KUBECONFIG' "${REGISTER}"; then
  echo 'registration script appears to print kubeconfig material' >&2
  exit 1
fi
echo 'managed cluster registration contract: PASS'
