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
run_check release_signature_contract bash deploy/scripts/test-release-evidence-contract.sh
run_check helm_lint helm lint --strict deploy/helm/aiops
run_check diff_check git diff --check

git_commit="$(git -C "${repo_root}" rev-parse HEAD)"
tree_digest="$(git -C "${repo_root}" diff --binary HEAD | shasum -a 256 | awk '{print $1}')"
if [[ -n "${AIOPS_RELEASE_UNIT_TESTS_COMMAND:-}" ]]; then
  run_check unit_tests bash -lc "${AIOPS_RELEASE_UNIT_TESTS_COMMAND}"
else
  # A release record must never imply that unit tests ran merely because the
  # static contracts passed.  CI can provide the exact locked test command.
  printf '%s\n' '{"name":"unit_tests","command":"<not supplied>","exit_code":null,"output_sha256":null,"status":"unverified"}' >>"${tmp_dir}/commands.jsonl"
fi

image_tag="${AIOPS_RELEASE_IMAGE_TAG:-${RELEASE_TAG:-git-${git_commit:0:12}}}"
image_registry="${AIOPS_IMAGE_REGISTRY:-${IMAGE_REGISTRY:-}}"
image_evidence="${tmp_dir}/images.json"
python3 - "${image_tag}" "${image_registry}" "${git_commit}" "${image_evidence}" \
  query-api ingest-pipeline event-collector ai-orchestrator observability-frontend \
  ai-action-executor ai-credential-broker ai-llm-egress-proxy schema-migrator \
  graph-schema-migrator clickhouse-migrator ipmi-exporter <<'PY'
import json
import os
import shutil
import subprocess
import sys

tag, registry, commit, output, *names = sys.argv[1:]
docker = shutil.which("docker")
records = []
for name in names:
    ref = f"{registry.rstrip('/')}/{name}:{tag}" if registry else f"{name}:{tag}"
    record = {
        "name": name,
        "ref": ref,
        "status": "unverified",
        "content_digest": None,
        "registry_digests": [],
        "revision_label": None,
        "revision_matches": False,
    }
    if docker:
        result = subprocess.run(
            [docker, "image", "inspect", ref, "--format", "{{json .}}"],
            capture_output=True,
            text=True,
        )
        if result.returncode == 0 and result.stdout.strip():
            try:
                image = json.loads(result.stdout)
                content_digest = image.get("Id")
                repo_digests = [
                    value for value in (image.get("RepoDigests") or [])
                    if "@sha256:" in value
                ]
                labels = (image.get("Config") or {}).get("Labels") or {}
                revision = labels.get("org.opencontainers.image.revision")
                record.update(
                    status="present",
                    content_digest=content_digest,
                    registry_digests=repo_digests,
                    revision_label=revision,
                    revision_matches=revision == commit,
                )
            except (json.JSONDecodeError, TypeError):
                record["status"] = "unverified"
    records.append(record)

all_present = all(item["status"] == "present" for item in records)
all_revision_matches = all(item["revision_matches"] for item in records)
all_registry_digests = all(bool(item["registry_digests"]) for item in records)
json.dump(
    {
        "tag": tag,
        "registry": registry or None,
        "registry_bound": bool(registry),
        "images": records,
        "all_present": all_present,
        "all_revision_labels_match": all_revision_matches,
        "all_registry_digests": all_registry_digests,
    },
    open(output, "w", encoding="utf-8"),
    ensure_ascii=False,
    indent=2,
)
PY

rendered_manifest="${AIOPS_RELEASE_RENDERED_MANIFEST:-}"
signature_file="${AIOPS_RELEASE_SIGNATURE_FILE:-}"
signature_public_key="${AIOPS_RELEASE_SIGNATURE_PUBLIC_KEY:-}"
python3 - "${repo_root}" "${out}" "${git_commit}" "${tree_digest}" "${tmp_dir}/commands.jsonl" \
  "${image_evidence}" "${rendered_manifest}" "${signature_file}" "${signature_public_key}" "${repo_root}/deploy/scripts/verify-release-signature.sh" <<'PY'
import json, pathlib, sys, datetime, hashlib
import subprocess
root, out, commit, tree_digest, commands_path, image_path, rendered_path, signature_path, public_key_path, verifier = sys.argv[1:]
commands = [json.loads(line) for line in pathlib.Path(commands_path).read_text().splitlines() if line.strip()]
checks = {}
for item in commands:
    if item.get("status") == "unverified" or item.get("exit_code") is None:
        checks[item["name"]] = "unverified"
    else:
        checks[item["name"]] = "pass" if item["exit_code"] == 0 else "fail"
dirty = bool(__import__("subprocess").run(["git", "-C", root, "status", "--porcelain"], capture_output=True, text=True).stdout.strip())
images = json.load(open(image_path, encoding="utf-8"))
def file_digest(path):
    if not path:
        return None
    candidate = pathlib.Path(path)
    if not candidate.is_file():
        return None
    return hashlib.sha256(candidate.read_bytes()).hexdigest()

rendered_digest = file_digest(rendered_path)
signature_digest = file_digest(signature_path)
public_key_digest = file_digest(public_key_path)
signature_status = "missing"
signature_reason = "signature, rendered manifest, or public key is missing"
if signature_digest and rendered_digest and public_key_digest:
    verify = subprocess.run(
        [verifier, rendered_path, signature_path, public_key_path],
        capture_output=True,
        text=True,
    )
    if verify.returncode == 0:
        signature_status = "verified"
        signature_reason = "detached Ed25519 signature verified"
    else:
        signature_status = "invalid"
        signature_reason = "detached signature verification failed"
elif signature_digest:
    signature_status = "present_unverified"
required_checks_pass = bool(checks) and all(value == "pass" for value in checks.values())
doc = {
    "schema_version": 1,
    "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "git_commit": commit,
    "working_tree_digest": tree_digest,
    "working_tree_dirty": dirty,
    "commands": commands,
    "checks": checks,
    "images": images,
    "release_materials": {
        "rendered_manifest": {"path": rendered_path or None, "sha256": rendered_digest, "status": "present" if rendered_digest else "unverified"},
        "signature": {
            "path": signature_path or None,
            "sha256": signature_digest,
            "public_key_path": public_key_path or None,
            "public_key_sha256": public_key_digest,
            "status": signature_status,
            "reason": signature_reason,
        },
    },
    # A local tag or BuildKit content digest is not a registry identity.  A
    # release is publishable only when CI supplies registry-bound digests,
    # a rendered manifest and a separately verified signature.
    "publishable": (
        not dirty
        and required_checks_pass
        and images.get("all_present") is True
        and images.get("all_revision_labels_match") is True
        and images.get("registry_bound") is True
        and images.get("all_registry_digests") is True
        and rendered_digest is not None
        and signature_status == "verified"
    ),
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
