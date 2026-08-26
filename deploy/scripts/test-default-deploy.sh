#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BUILD="$ROOT/deploy/scripts/build-images.sh"
APPLY="$ROOT/deploy/scripts/apply.sh"
CHART="$ROOT/deploy/helm/aiops/Chart.yaml"
VALUES="$ROOT/deploy/helm/aiops/values.yaml"

grep -q 'build ai-event-collector event-collector' "$BUILD"
grep -q 'SKIP_IMAGE_BUILD' "$APPLY"
grep -q 'build-images.sh' "$APPLY"
grep -q 'global.imageTag' "$APPLY"
grep -q 'appVersion: "1.1.1"' "$CHART"
grep -q 'imageTag: "v1.1.1"' "$VALUES"

version_file="$(mktemp)"
trap 'rm -f "$version_file"' EXIT
ROOT="$ROOT" AIOPS_VERSION_FILE="$version_file" IMAGE_TAG="" RELEASE_TAG="" bash -c '
  set -euo pipefail
  source "$1/deploy/scripts/version.sh"
  expected="git-$(git -C "$1" rev-parse --short=12 HEAD)"
  first="$(resolve_image_tag)"
  [[ "$first" == "$expected" ]]
  record_deployed_version "$first"
  [[ ! -e "$AIOPS_VERSION_FILE" ]]
  IMAGE_TAG="git-explicit"
  RELEASE_TAG="v9.9.9"
  [[ "$(resolve_image_tag)" == "git-explicit" ]]
  IMAGE_TAG=""
  [[ "$(resolve_image_tag)" == "v9.9.9" ]]
' _ "$ROOT"

echo "default deployment flow checks passed"
