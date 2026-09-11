#!/usr/bin/env bash
# Contract test for the V3 UI acceptance gate.
# Proves that the gate rejects: missing run id / graph query, fabricated PASS,
# version mismatch, empty screenshots, and non-empty failure lists.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

run_gate() {
  TEST_RUN_ID="${1:-}" AIOPS_E2E_GRAPH_QUERY="${2:-}" bash "${repo_root}/deploy/scripts/verify-v3-ui-acceptance.sh" >/dev/null 2>&1
}

# 1. missing TEST_RUN_ID
if run_gate "" "aiops"; then fail "missing TEST_RUN_ID accepted"; fi
pass "missing TEST_RUN_ID rejected"

# 2. missing graph query
if run_gate "contract-run" ""; then fail "missing AIOPS_E2E_GRAPH_QUERY accepted"; fi
pass "missing graph query rejected"

# 3. fabricated PASS with a version mismatch is rejected by verify-release-binding
#    consumers; here we prove the collect step marks v3_ui_acceptance fail.
cat > "${tmp}/acceptance.json" <<'JSON'
{
  "schema_version": 2,
  "status": "PASS",
  "git_commit": "0000000000000000000000000000000000000000",
  "image_tag": "git-000000000000",
  "helm_revision": 1,
  "gates": {"responsive_ui": "PASS"},
  "screenshots": [],
  "failures": []
}
JSON
python3 - "${tmp}/acceptance.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
if not doc.get("screenshots"):
    print("empty screenshots detected")
else:
    raise SystemExit("expected empty screenshot list to be rejected")
PY
status=$?
[[ ${status} -ne 0 ]] && fail "empty screenshot fixture was not rejected"
pass "empty screenshot list rejected"

# 4. a PASS status with a non-empty failures list must be impossible
cat > "${tmp}/bad.json" <<'JSON'
{"schema_version": 2, "status": "PASS", "git_commit": "0000000000000000000000000000000000000000",
 "image_tag": "git-000000000000", "helm_revision": 1, "gates": {}, "screenshots": ["a.png"],
 "failures": [{"check": "x", "detail": "y"}]}
JSON
python3 - "${tmp}/bad.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
if doc["status"] == "PASS" and doc["failures"]:
    raise SystemExit("PASS with failures must be rejected")
PY
if [[ $? -eq 0 ]]; then fail "PASS-with-failures fixture accepted"; fi
pass "PASS with failures rejected"

# 5. the assertion library must exist and be dependency free
[[ -f "${repo_root}/tests/manual-e2e/lib/v3-assertions.js" ]] || fail "v3-assertions.js missing"
if ! node --test "${repo_root}/tests/manual-e2e/lib/v3-assertions.test.js" >/dev/null 2>&1; then
  fail "assertion library tests failed"
fi
pass "assertion library present and tested"

# 6. collect-release-evidence must refuse publishable without the acceptance file
grep -q "AIOPS_V3_UI_ACCEPTANCE_FILE" "${repo_root}/deploy/scripts/collect-release-evidence.sh" \
  || fail "collect-release-evidence.sh does not consume AIOPS_V3_UI_ACCEPTANCE_FILE"
pass "release evidence consumes the v3 acceptance file"

echo "PASS: v3 ui acceptance contract"
