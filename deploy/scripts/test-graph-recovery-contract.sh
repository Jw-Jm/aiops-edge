#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="${repo_root}/deploy/scripts/graph-recovery-test.sh"
tmp_output="${TMPDIR:-/tmp}/aiops-recovery-contract.$$.json"
trap 'rm -f "${tmp_output}"' EXIT

if ! rg -n 'GRAPH_RECOVERY_QUERY_API_DEPLOYMENT:-query-api-http' "${script}" >/dev/null; then
  echo "recovery contract failed: default Query API deployment name is not query-api-http" >&2
  exit 1
fi
if ! rg -n 'scale "deployment/\$\{query_api_deployment\}"|rollout status "deployment/\$\{query_api_deployment\}"|exec "deploy/\$\{query_api_deployment\}"' "${script}" >/dev/null; then
  echo "recovery contract failed: recovery script does not use configurable Query API deployment" >&2
  exit 1
fi
if ! rg -n 'scale statefulset/hugegraph --replicas=0|wait --for=delete pod -l app=hugegraph' "${script}" >/dev/null; then
  echo "recovery contract failed: HugeGraph StatefulSet is not stopped before PVC deletion" >&2
  exit 1
fi
if ! rg -n 'wget --no-check-certificate -q -O - https://127\.0\.0\.1:8080/readyz' "${script}" >/dev/null || \
   ! rg -n 'Authorization: Basic \$\{AUTH\}' "${script}" >/dev/null; then
  echo "recovery contract failed: post-recovery readiness probes are not authenticated" >&2
  exit 1
fi

if GRAPH_RECOVERY_ENV=production GRAPH_RECOVERY_CONFIRM=I_UNDERSTAND_LOCAL_GRAPH_RECOVERY bash "${script}" --inject --output "${tmp_output}" >/dev/null 2>&1; then
  echo "recovery contract failed: production injection was accepted" >&2
  exit 1
fi
if GRAPH_RECOVERY_ENV=local GRAPH_RECOVERY_CONTEXT=production GRAPH_RECOVERY_CONFIRM=I_UNDERSTAND_LOCAL_GRAPH_RECOVERY bash "${script}" --inject --output "${tmp_output}" >/dev/null 2>&1; then
  echo "recovery contract failed: production context injection was accepted" >&2
  exit 1
fi
if GRAPH_RECOVERY_ENV=local GRAPH_RECOVERY_CONFIRM=I_UNDERSTAND_LOCAL_GRAPH_RECOVERY GRAPH_RECOVERY_DRY_RUN=1 \
  bash "${script}" --inject --output "${tmp_output}" >/dev/null 2>&1; then
  python3 - "${tmp_output}" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert data["recovery_test"] == "dry_run"
assert data["mutation"] == "planned"
assert data["environment"] == "local"
PY
else
  echo "recovery contract failed: guarded local dry-run was not accepted" >&2
  exit 1
fi

offline_output="${TMPDIR:-/tmp}/aiops-recovery-contract-offline.$$.out"
trap 'rm -f "${tmp_output}" "${offline_output}"' EXIT
bash "${script}" --offline >"${offline_output}"
if ! python3 - "${offline_output}" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert data["mutation"] == "none"
PY
then
  echo "recovery contract failed: offline mode is not read-only" >&2
  exit 1
fi
echo "graph recovery contract tests passed"
