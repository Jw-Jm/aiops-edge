#!/usr/bin/env bash
set -euo pipefail

# Import an existing local validation Secret without printing its values. This
# preserves the MySQL account passwords already bound to an existing PVC while
# adding a generated HugeGraph password when the older release has no graph
# Secret yet.

usage() {
  echo "Usage: import-local-secrets-from-k8s.sh --namespace NAMESPACE --secret NAME --output PATH"
}

namespace=""
secret=""
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace) namespace="${2:?--namespace requires a value}"; shift 2 ;;
    --secret) secret="${2:?--secret requires a value}"; shift 2 ;;
    --output) output="${2:?--output requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

[[ -n "${namespace}" && -n "${secret}" && -n "${output}" ]] || { usage >&2; exit 2; }
[[ ! -e "${output}" ]] || { echo "refusing to overwrite existing file: ${output}" >&2; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "missing required command: kubectl" >&2; exit 2; }
command -v base64 >/dev/null 2>&1 || { echo "missing required command: base64" >&2; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "missing required command: openssl" >&2; exit 2; }

mkdir -p "$(dirname "${output}")"
tmp_output="${output}.tmp.$$"
trap 'rm -f "${tmp_output}"' EXIT
umask 077

secret_value() {
  local key="$1" encoded
  encoded="$(kubectl -n "${namespace}" get secret "${secret}" -o "jsonpath={.data.${key}}")"
  [[ -n "${encoded}" ]] || return 1
  printf '%s' "${encoded}" | base64 -D
}

required_keys=(
  JWT_SECRET LLM_ENCRYPTION_KEY INTERNAL_TOKEN INGEST_API_KEY
  CLICKHOUSE_PASSWORD MYSQL_ROOT_PASSWORD MYSQL_APP_PASSWORD MYSQL_MIGRATOR_PASSWORD
  ADMIN_INITIAL_PASSWORD LLM_PROXY_TOKEN LLM_PROVIDER_KEYS
  ORCHESTRATOR_TO_QUERY_TOKEN ORCHESTRATOR_TO_QUERY_SIGNING_KEY ORCHESTRATOR_TO_QUERY_VERIFY_KEYS
  QUERY_TO_ORCHESTRATOR_TOKEN QUERY_TO_ORCHESTRATOR_SIGNING_KEY QUERY_TO_ORCHESTRATOR_VERIFY_KEYS
  EXECUTOR_TOKEN AI_ACTION_EXECUTOR_SIGNING_KEY AI_ACTION_EXECUTOR_VERIFY_KEYS
)
for key in "${required_keys[@]}"; do
  value="$(secret_value "${key}")" || { echo "Kubernetes Secret is missing ${key}" >&2; exit 1; }
  printf 'export %s=%q\n' "${key}" "${value}" >>"${tmp_output}"
done

if value="$(secret_value HUGEGRAPH_PASSWORD 2>/dev/null)" && [[ -n "${value}" ]]; then
  printf 'export HUGEGRAPH_PASSWORD=%q\n' "${value}" >>"${tmp_output}"
else
  printf 'export HUGEGRAPH_PASSWORD=%q\n' "$(openssl rand -hex 24)" >>"${tmp_output}"
fi

chmod 600 "${tmp_output}"
mv "${tmp_output}" "${output}"
printf '%s\n' "${output}"
