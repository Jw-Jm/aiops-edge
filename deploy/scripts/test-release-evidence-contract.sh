#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
verifier="${repo_root}/deploy/scripts/verify-release-signature.sh"
binding_verifier="${repo_root}/deploy/scripts/verify-release-binding.sh"
tmp_dir="$(mktemp -d /tmp/aiops-release-signature.XXXXXX)"
trap 'rm -rf "${tmp_dir}"' EXIT

command -v openssl >/dev/null 2>&1 || { echo "missing openssl" >&2; exit 2; }
printf '%s\n' 'rendered release manifest: commit=contract' >"${tmp_dir}/manifest.yaml"
openssl genpkey -algorithm Ed25519 -out "${tmp_dir}/private.pem" >/dev/null 2>&1
openssl pkey -in "${tmp_dir}/private.pem" -pubout -out "${tmp_dir}/public.pem" >/dev/null 2>&1
openssl pkeyutl -sign -inkey "${tmp_dir}/private.pem" -rawin \
  -in "${tmp_dir}/manifest.yaml" -out "${tmp_dir}/manifest.sig"

"${verifier}" "${tmp_dir}/manifest.yaml" "${tmp_dir}/manifest.sig" "${tmp_dir}/public.pem" >/dev/null
printf '%s\n' 'tampered' >>"${tmp_dir}/manifest.yaml"
if "${verifier}" "${tmp_dir}/manifest.yaml" "${tmp_dir}/manifest.sig" "${tmp_dir}/public.pem" >/dev/null 2>&1; then
  echo "tampered release manifest unexpectedly verified" >&2
  exit 1
fi

cat >"${tmp_dir}/images.json" <<'JSON'
{
  "tag": "git-contract",
  "registry": "registry.example.invalid/aiops",
  "registry_bound": true,
  "images": [
    {
      "name": "query-api",
      "registry_digests": ["registry.example.invalid/aiops/query-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],
      "revision_label": "1111111111111111111111111111111111111111",
      "revision_matches": true
    },
    {
      "name": "ai-orchestrator",
      "registry_digests": ["registry.example.invalid/aiops/ai-orchestrator@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
      "revision_label": "1111111111111111111111111111111111111111",
      "revision_matches": true
    }
  ]
}
JSON
cat >"${tmp_dir}/binding.json" <<'JSON'
{
  "schema_version": 1,
  "git_commit": "1111111111111111111111111111111111111111",
  "image_tag": "git-contract",
  "rendered_manifest_sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
  "images": [
    {"name": "query-api", "immutable_digest": "registry.example.invalid/aiops/query-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
    {"name": "ai-orchestrator", "immutable_digest": "registry.example.invalid/aiops/ai-orchestrator@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
  ],
  "migration_digests": ["dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"],
  "policy_digests": ["eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"],
  "data_digests": ["ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"]
}
JSON
"${binding_verifier}" "${tmp_dir}/binding.json" \
  1111111111111111111111111111111111111111 git-contract \
  cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  "${tmp_dir}/images.json" >/dev/null

# A validly signed binding must still be rejected when its release identity is
# changed. This catches signature replay across commits/tags.
cp "${tmp_dir}/binding.json" "${tmp_dir}/binding-tampered.json"
python3 - "${tmp_dir}/binding-tampered.json" <<'PY'
import json
import sys

path = sys.argv[1]
doc = json.load(open(path, encoding="utf-8"))
doc["git_commit"] = "2222222222222222222222222222222222222222"
open(path, "w", encoding="utf-8").write(json.dumps(doc))
PY
if "${binding_verifier}" "${tmp_dir}/binding-tampered.json" \
  1111111111111111111111111111111111111111 git-contract \
  cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  "${tmp_dir}/images.json" >/dev/null 2>&1; then
  echo "tampered release binding unexpectedly verified" >&2
  exit 1
fi
echo "release evidence signature contract tests passed"
