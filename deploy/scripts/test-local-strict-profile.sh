#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART_DIR="${ROOT_DIR}/deploy/helm/aiops"
OVERLAY="${CHART_DIR}/values-local-strict-test.yaml"
OUT="$(mktemp "${TMPDIR:-/tmp}/aiops-strict-profile.XXXXXX")"
trap 'rm -f "${OUT}"' EXIT

[[ -f "${OVERLAY}" ]] || { echo 'strict local values overlay is missing' >&2; exit 1; }
for required in \
  'llmMock: "false"' \
  'investigationRuntimeEnabled: "0"' \
  'enabled: true' \
  'executionMode: approved' \
  'realMutation: true' \
  'aiops-action-test' \
  'enabled: false'; do
  grep -Fq "${required}" "${OVERLAY}" || { echo "strict profile missing: ${required}" >&2; exit 1; }
done

helm template aiops "${CHART_DIR}" \
  -f "${CHART_DIR}/values.yaml" \
  -f "${CHART_DIR}/values-local-validation.yaml" \
  -f "${OVERLAY}" \
  --set global.imageTag=local-strict \
  --set secrets.existingSecretName=aiops-secrets > "${OUT}"

grep -Fq 'name: ai-llm-egress-proxy' "${OUT}" || { echo 'strict profile did not render the LLM proxy' >&2; exit 1; }
grep -Fq 'name: ai-investigation-worker' "${OUT}" || { echo 'strict profile did not render the investigation worker' >&2; exit 1; }
grep -Fq 'name: LLM_MOCK' "${OUT}" || { echo 'strict profile missing LLM_MOCK wiring' >&2; exit 1; }
grep -Fq 'value: "false"' "${OUT}" || { echo 'strict profile must disable LLM mock' >&2; exit 1; }
grep -Fq 'name: EXECUTION_MODE' "${OUT}" || { echo 'strict profile missing approved executor' >&2; exit 1; }
grep -Fq 'value: "approved"' "${OUT}" || { echo 'strict profile executor is not approved' >&2; exit 1; }
grep -Fq 'name: LLM_PROVIDER_KEYS' "${OUT}" || { echo 'provider key must be injected only into proxy' >&2; exit 1; }
if grep -n 'name: aiops-loadgen' "${OUT}"; then
  echo 'strict profile must not deploy a long-running load generator' >&2
  exit 1
fi
echo 'local strict profile: PASS'
