#!/usr/bin/env bash
set -euo pipefail

# P1-SCA1: dependency vulnerability gate (no || true, no log-only mode).
# Blocking rule: frontend production dependencies (npm --omit=dev) must have
# critical=0 and high=0 (allowlist: docs/remediation/.../SCA_TRIAGE_LEDGER.md).
# Python (pip-audit) and Go (govulncheck) must be installed; a missing scanner
# fails the gate instead of silently skipping.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

fail=0

echo "[sca] frontend production-dependency audit"
cd observability-frontend
if [[ ! -d node_modules ]]; then
  npm ci --silent
fi
# npmmirror does not implement the audit endpoint; the advisory lookup is a
# metadata query against the official registry and does not change package
# resolution (installs keep using the configured mirror).
# NOTE: npm audit exits non-zero when vulnerabilities exist; the gate decision
# is made below from the advisory counts, so the exit code is intentionally
# captured instead of aborting (this is not an error-suppressing `|| true`:
# empty/missing output still fails the gate).
audit_rc=0
npm audit --registry=https://registry.npmjs.org/ --omit=dev --json > /tmp/aiops-npm-audit-prod.json || audit_rc=$?
if [[ ! -s /tmp/aiops-npm-audit-prod.json ]]; then
  echo "npm audit returned no data (registry unreachable) — cannot gate" >&2
  exit 1
fi
audit_json="$(cat /tmp/aiops-npm-audit-prod.json)"
rm -f /tmp/aiops-npm-audit-prod.json
RESULT="$(NPM_AUDIT_JSON="${audit_json}" python3 - <<'PYEOF'
import json, os, sys
d = json.loads(os.environ["NPM_AUDIT_JSON"])
v = d.get("metadata", {}).get("vulnerabilities", {})
print(f"{v.get('critical', 0)} {v.get('high', 0)}")
bad = [(vid, adv) for vid, adv in d.get("vulnerabilities", {}).items()
       if adv.get("severity") in ("critical", "high")]
for vid, adv in bad:
    via = [x.get("title", "?") if isinstance(x, dict) else x for x in adv.get("via", [])]
    print(f"  [{adv['severity']}] {vid} range={adv.get('range')} via={via[:2]}", file=sys.stderr)
PYEOF
)"
critical="${RESULT%% *}"
high="${RESULT##* }"
echo "[sca] frontend production vulnerabilities: critical=${critical} high=${high}"
if [[ "${critical}" != "0" || "${high}" != "0" ]]; then
  echo "SCA GATE FAILED: production critical/high must be 0 (allowlist policy: docs/remediation/2026-09-03/phase1-p1sca1/SCA_TRIAGE_LEDGER.md)" >&2
  fail=1
fi
cd "${repo_root}"

echo "[sca] python (pip-audit on requirements-lock.txt)"
if [[ -x ai-orchestrator/.venv-ci/bin/pip-audit ]]; then
  PIP_AUDIT=(ai-orchestrator/.venv-ci/bin/pip-audit)
elif [[ -n "${AIOPS_PIP_AUDIT:-}" ]]; then
  read -ra PIP_AUDIT <<< "${AIOPS_PIP_AUDIT}"
elif command -v pip-audit >/dev/null 2>&1; then
  PIP_AUDIT=(pip-audit)
else
  echo "pip-audit not installed — install with: pip install pip-audit" >&2
  exit 1
fi
# --disable-pip + --no-deps: audit the pinned lock entries directly, no
# temporary venv / dependency resolution required.
# pip-audit exits non-zero when vulnerabilities exist; the allowlist decision
# is made below, so the exit code is captured here (missing output fails).
pip_audit_json=""
pip_audit_rc=0
pip_audit_json="$("${PIP_AUDIT[@]}" --disable-pip --no-deps --format json -r ai-orchestrator/requirements-lock.txt)" || pip_audit_rc=$?
if [[ -z "${pip_audit_json}" ]]; then
  echo "pip-audit returned no data — cannot gate" >&2
  exit 1
fi
PIP_AUDIT_JSON="${pip_audit_json}" python3 - <<'PYEOF' || fail=1
import datetime, json, os, sys
d = json.loads(os.environ["PIP_AUDIT_JSON"])
today = datetime.date.today()
allow = {}
for line in open("deploy/scripts/sca-allowlist.txt"):
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    adv_id, expiry, reason = (line.split("|", 2) + ["", ""])[:3]
    allow[adv_id] = (expiry, reason)

failures = []
for dep in d.get("dependencies", []):
    for vuln in dep.get("vulns", []):
        vid = vuln.get("id", "")
        fix = ",".join(vuln.get("fix_versions", []) or [])
        if vid in allow:
            expiry, reason = allow[vid]
            if datetime.date.fromisoformat(expiry) < today:
                failures.append(f"{vid} allowlist EXPIRED ({expiry}) — renew risk acceptance or upgrade")
            else:
                print(f"  [allowlisted until {expiry}] {vid} on {dep['name']}=={dep['version']}")
        else:
            failures.append(f"{vid} on {dep['name']}=={dep['version']} fix={fix or 'none'}")
if failures:
    for f in failures:
        print(f"  VULN: {f}", file=sys.stderr)
    print("python SCA gate failed (unallowlisted vulnerabilities)" if any(
        f.startswith('VULN') for f in failures) else "python SCA gate failed", file=sys.stderr)
    sys.exit(1)
print("  python dependency audit clean (or all findings allowlisted)")
PYEOF

echo "[sca] go (govulncheck)"
if command -v govulncheck >/dev/null 2>&1; then
  for repo in ai-apm-query-go ai-action-executor ai-credential-broker ai-llm-egress-proxy ai-event-collector ai-apm-ingest-go; do
    [[ -d "${repo}" ]] || continue
    echo "[sca] govulncheck ${repo}"
    (cd "${repo}" && govulncheck ./...) || fail=1
  done
else
  echo "govulncheck not installed — install with: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2
  exit 1
fi

if [[ "${fail}" != "0" ]]; then
  echo "SCA gate failed" >&2
  exit 1
fi
echo "[sca] dependency vulnerability gate passed"
