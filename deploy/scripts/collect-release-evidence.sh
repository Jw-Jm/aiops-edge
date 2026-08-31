#!/usr/bin/env bash
set -euo pipefail

# Generate a version-bound, machine-readable release evidence record. The
# script never edits source files or contacts a cluster. A dirty tree is
# recorded explicitly and cannot be mistaken for a publishable commit.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out="${1:-${repo_root}/deploy/evidence/release-evidence.json}"
mkdir -p "$(dirname "${out}")"
tmp_dir="$(mktemp -d /tmp/aiops-evidence.XXXXXX)"
trap 'rm -rf "${tmp_dir}"' EXIT

run_check() {
  local name="$1"; shift
  local output_file="${tmp_dir}/${name}.out"
  set +e
  (cd "${repo_root}" && "$@") >"${output_file}" 2>&1
  local rc=$?
  set -e
  local digest
  digest="$(shasum -a 256 "${output_file}" | awk '{print $1}')"
  python3 - "${name}" "$*" "${rc}" "${digest}" "${output_file}" >>"${tmp_dir}/commands.jsonl" <<'PY'
import json, sys
name, command, rc, digest, output = sys.argv[1:]
print(json.dumps({"name": name, "command": command, "exit_code": int(rc), "output_sha256": digest, "output_file": output}, ensure_ascii=False))
PY
  return 0
}

run_check deployment_contract bash deploy/scripts/test-deployment-contracts.sh
run_check production_architecture env AIOPS_CONTRACT_ALLOW_TEST_SECRETS=true bash deploy/scripts/test-production-architecture-contracts.sh
run_check helm_lint helm lint --strict deploy/helm/aiops
run_check diff_check git diff --check

git_commit="$(git -C "${repo_root}" rev-parse HEAD)"
tree_digest="$(git -C "${repo_root}" diff --binary HEAD | shasum -a 256 | awk '{print $1}')"
python3 - "${repo_root}" "${out}" "${git_commit}" "${tree_digest}" "${tmp_dir}/commands.jsonl" <<'PY'
import json, pathlib, sys, datetime, hashlib
root, out, commit, tree_digest, commands_path = sys.argv[1:]
commands = [json.loads(line) for line in pathlib.Path(commands_path).read_text().splitlines() if line.strip()]
checks = {}
for item in commands:
    checks[item["name"]] = "pass" if item["exit_code"] == 0 else "fail"
dirty = bool(__import__("subprocess").run(["git", "-C", root, "status", "--porcelain"], capture_output=True, text=True).stdout.strip())
doc = {
    "schema_version": 1,
    "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "git_commit": commit,
    "working_tree_digest": tree_digest,
    "working_tree_dirty": dirty,
    "commands": commands,
    "checks": checks,
    "publishable": (not dirty and all(v == "pass" for v in checks.values())),
}
pathlib.Path(out).write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({"output": out, "git_commit": commit, "working_tree_dirty": dirty, "publishable": doc["publishable"]}, ensure_ascii=False))
PY

if [[ "${AIOPS_EVIDENCE_VALIDATE:-true}" == "true" ]]; then
  python3 - "${out}" "${repo_root}/deploy/evidence/release-evidence.schema.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
schema = json.load(open(sys.argv[2], encoding="utf-8"))
for key in schema["required"]:
    if key not in doc: raise SystemExit(f"missing evidence field: {key}")
if doc.get("schema_version") != 1: raise SystemExit("unsupported evidence schema")
if len(doc.get("git_commit", "")) != 40: raise SystemExit("invalid git commit")
PY
fi
