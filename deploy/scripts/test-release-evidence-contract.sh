#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
verifier="${repo_root}/deploy/scripts/verify-release-signature.sh"
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
echo "release evidence signature contract tests passed"
