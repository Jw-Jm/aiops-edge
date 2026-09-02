#!/usr/bin/env bash
set -euo pipefail

# Validate that a signed release binding describes the exact release evidence
# collected from this checkout.  Signature verification is deliberately kept
# separate (verify-release-signature.sh); this script validates the signed
# payload's semantic identity and prevents a valid signature from being
# replayed for another commit, image set, or rendered manifest.

binding="${1:-}"
expected_commit="${2:-}"
expected_tag="${3:-}"
expected_rendered_digest="${4:-}"
images_evidence="${5:-}"

if [[ -z "${binding}" || -z "${expected_commit}" || -z "${expected_tag}" || -z "${expected_rendered_digest}" || -z "${images_evidence}" ]]; then
  echo "usage: verify-release-binding.sh BINDING EXPECTED_COMMIT EXPECTED_TAG EXPECTED_RENDERED_SHA256 IMAGES_EVIDENCE" >&2
  exit 2
fi
for path in "${binding}" "${images_evidence}"; do
  [[ -f "${path}" ]] || {
    echo "release binding input is not a regular file: ${path}" >&2
    exit 2
  }
done

python3 - "${binding}" "${expected_commit}" "${expected_tag}" "${expected_rendered_digest}" "${images_evidence}" <<'PY'
import hashlib
import json
import re
import sys

binding_path, expected_commit, expected_tag, expected_rendered, images_path = sys.argv[1:]
try:
    binding = json.load(open(binding_path, encoding="utf-8"))
    images = json.load(open(images_path, encoding="utf-8"))
except (OSError, json.JSONDecodeError) as exc:
    raise SystemExit(f"invalid release binding JSON: {exc}")

if not isinstance(binding, dict) or binding.get("schema_version") != 1:
    raise SystemExit("unsupported release binding schema")
if binding.get("git_commit") != expected_commit:
    raise SystemExit("release binding git_commit does not match HEAD")
if binding.get("image_tag") != expected_tag:
    raise SystemExit("release binding image_tag does not match collected image tag")
if binding.get("rendered_manifest_sha256") != expected_rendered:
    raise SystemExit("release binding rendered manifest digest does not match")

sha256_re = re.compile(r"^[0-9a-f]{64}$")
for field in ("migration_digests", "policy_digests", "data_digests"):
    values = binding.get(field)
    if not isinstance(values, list) or not values:
        raise SystemExit(f"release binding {field} must be a non-empty list")
    if any(not isinstance(value, str) or not sha256_re.fullmatch(value) for value in values):
        raise SystemExit(f"release binding {field} contains a non-SHA256 digest")

expected_images = images.get("images")
if not isinstance(expected_images, list) or not expected_images:
    raise SystemExit("collected image evidence is empty")
bound_images = binding.get("images")
if not isinstance(bound_images, list):
    raise SystemExit("release binding images must be a list")

bound_by_name = {}
for item in bound_images:
    if not isinstance(item, dict) or not isinstance(item.get("name"), str):
        raise SystemExit("release binding contains an invalid image entry")
    if item["name"] in bound_by_name:
        raise SystemExit(f"release binding duplicates image {item['name']}")
    digest = item.get("immutable_digest")
    if not isinstance(digest, str) or "@sha256:" not in digest:
        raise SystemExit(f"release binding image {item['name']} lacks immutable digest")
    if not sha256_re.fullmatch(digest.rsplit("@sha256:", 1)[1]):
        raise SystemExit(f"release binding image {item['name']} has invalid immutable digest")
    bound_by_name[item["name"]] = item

if set(bound_by_name) != {item.get("name") for item in expected_images}:
    raise SystemExit("release binding image set does not match collected image set")
for expected in expected_images:
    name = expected.get("name")
    item = bound_by_name[name]
    registry_digests = expected.get("registry_digests") or []
    immutable_digest = item["immutable_digest"]
    if immutable_digest not in registry_digests:
        raise SystemExit(f"release binding image {name} digest is not a collected registry digest")
    if expected.get("revision_label") != expected_commit or not expected.get("revision_matches"):
        raise SystemExit(f"collected image {name} revision label does not match HEAD")

# Include a deterministic content hash in the contract output so callers can
# archive the exact signed payload without exposing any secret material.
payload_digest = hashlib.sha256(open(binding_path, "rb").read()).hexdigest()
print(json.dumps({"binding": "verified", "payload_sha256": payload_digest}, ensure_ascii=False))
PY
