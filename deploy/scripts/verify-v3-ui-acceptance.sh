#!/usr/bin/env bash
# V3 UI acceptance gate (G11 browser binding).
#
# Requires:
#   TEST_RUN_ID                 run identity, also the evidence directory name
#   AIOPS_E2E_GRAPH_QUERY       a real resource name present in the target cluster
# Optional:
#   AIOPS_RELEASE_IMAGE_TAG     overrides image-tag binding
#   AIOPS_HELM_REVISION         overrides helm revision binding
#   AIOPS_ROLLBACK_REVISION     overrides rollback revision binding
#
# The gate fails unless the produced JSON reports status=PASS.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "${TEST_RUN_ID:-}" ]]; then
  echo "FAIL: TEST_RUN_ID is required" >&2
  exit 1
fi
if [[ -z "${AIOPS_E2E_GRAPH_QUERY:-}" ]]; then
  echo "FAIL: AIOPS_E2E_GRAPH_QUERY is required (graph gate refuses an implicit pass)" >&2
  exit 1
fi

export TEST_RUN_ID
export AIOPS_E2E_GRAPH_QUERY

echo "==> running v3 browser acceptance (run=${TEST_RUN_ID})"
node "${repo_root}/tests/manual-e2e/v3-acceptance.js"

result="${repo_root}/test-results/${TEST_RUN_ID}/v3-acceptance.json"
if [[ ! -s "${result}" ]]; then
  echo "FAIL: acceptance result missing: ${result}" >&2
  exit 1
fi

python3 - "${result}" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
if doc.get("status") != "PASS":
    print("FAIL: v3_ui_acceptance status=%s" % doc.get("status"))
    for item in doc.get("failures", []):
        print("  - %s %s %s" % (item.get("viewport", ""), item.get("check", ""), item.get("detail", "")))
    raise SystemExit(1)
if doc.get("schema_version") != 2:
    raise SystemExit("FAIL: unsupported acceptance schema version")
for key in ("git_commit", "image_tag", "helm_revision"):
    if not doc.get(key):
        raise SystemExit("FAIL: acceptance result missing %s" % key)
if not doc.get("screenshots"):
    raise SystemExit("FAIL: acceptance result has no screenshots")
if len(doc.get("git_commit", "")) != 40:
    raise SystemExit("FAIL: acceptance git_commit must be a 40-char commit")
print("PASS: v3_ui_acceptance commit=%s tag=%s helm=%s screenshots=%d" % (
    doc["git_commit"], doc["image_tag"], doc["helm_revision"], len(doc["screenshots"])))
PY

echo "==> assistant compact-desktop gate"
node "${repo_root}/tests/manual-e2e/ia-assistant-v3.js"
echo "PASS: v3 ui acceptance completed"
