#!/usr/bin/env bash
set -euo pipefail

# Static/isolated contract checks for the read-only online validator.  Live
# Kubernetes checks are intentionally exercised by validate-local-stack.sh;
# this gate ensures future edits cannot silently remove a mandatory matrix row.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
validator="${repo_root}/deploy/scripts/validate-local-stack.sh"
values="${repo_root}/deploy/helm/aiops/values.yaml"
local_values="${repo_root}/deploy/helm/aiops/values-local-validation.yaml"
dry_run="${TMPDIR:-/tmp}/aiops-local-validation-dry-run.$$.out"
trap 'rm -f "${dry_run}"' EXIT

for required in \
  'authRequireFirstLoginPasswordChange: true' \
  'adminInitialPassword: "admin1234"'; do
  rg -n --fixed-strings "${required}" "${values}" >/dev/null || {
    echo "local validation contract failed: missing ${required}" >&2
    exit 1
  }
done
rg -n --fixed-strings 'authRequireFirstLoginPasswordChange: false' "${local_values}" >/dev/null || {
  echo "local validation contract failed: local first-login bypass is missing" >&2
  exit 1
}

required_strings=(
  "aiops_schema_migrations"
  "0009_action_workflow_closure"
  "0016_ai_chat_turn_id"
  "ai_chat_messages.turn_id"
  "uq_ai_chat_message_turn"
  "MYSQL_APP_PASSWORD"
  "MYSQL_MIGRATOR_PASSWORD"
  "app=ai-investigation-worker"
  "app=ai-llm-egress-proxy"
  "LLM_PROVIDER_KEYS"
  "LEGACY_FLOW_RUNTIME_ENABLED"
  "LEGACY_DIRECT_MUTATIONS_ENABLED"
  "kubectl auth can-i get deployments"
  "kubectl auth can-i patch deployments"
  "validate-observability-evidence.sh"
  "BLOCKED_BY_ENV"
)
for required in "${required_strings[@]}"; do
  rg -n --fixed-strings "${required}" "${validator}" >/dev/null || {
    echo "local validation contract failed: missing ${required}" >&2
    exit 1
  }
done

bash "${repo_root}/deploy/scripts/local-validation.sh" --dry-run --skip-deepflow >"${dry_run}"
awk '
  /\[0\] generate secrets/ { s0=NR }
  /\[1\] build/ { s1=NR }
  /\[2\] helm/ { s2=NR }
  /\[3\] bootstrap/ { s3=NR }
  /\[4\] wait/ { s4=NR }
  /\[5\] runtime/ { s5=NR }
  /\[6\] canary/ { s6=NR }
  END { exit !(s0 < s1 && s1 < s2 && s2 < s3 && s3 < s4 && s4 < s5 && s5 < s6) }
' "${dry_run}"

if bash "${repo_root}/deploy/scripts/local-validation.sh" --destroy >/dev/null 2>&1; then
  echo "local validation contract failed: --destroy without confirmation succeeded" >&2
  exit 1
fi

echo "local validation contract tests passed"
