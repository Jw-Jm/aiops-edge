#!/usr/bin/env bash
set -euo pipefail

# Verify a detached Ed25519 signature over a rendered release manifest.
# The verifier is intentionally small and depends only on the OpenSSL
# executable shipped by the build/release environment.  It never treats an
# environment flag as proof of verification.

manifest="${1:-}"
signature="${2:-}"
public_key="${3:-}"

if [[ -z "${manifest}" || -z "${signature}" || -z "${public_key}" ]]; then
  echo "usage: verify-release-signature.sh MANIFEST SIGNATURE PUBLIC_KEY" >&2
  exit 2
fi
for path in "${manifest}" "${signature}" "${public_key}"; do
  [[ -f "${path}" ]] || {
    echo "release signature input is not a regular file: ${path}" >&2
    exit 2
  }
done
command -v openssl >/dev/null 2>&1 || {
  echo "openssl is required for release signature verification" >&2
  exit 2
}

# -rawin is required for Ed25519 in OpenSSL 3.x; it verifies the exact bytes
# on disk, so a rendered-manifest change cannot be hidden by a stale signature.
openssl pkeyutl -verify -pubin -inkey "${public_key}" \
  -sigfile "${signature}" -rawin -in "${manifest}" >/dev/null
echo "release signature verified"
