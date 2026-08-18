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
ROOT="$ROOT" AIOPS_VERSION_FILE="$version_file" IMAGE_TAG="" bash -c '
  source "$1/deploy/scripts/version.sh"
  first="$(resolve_image_tag)"
  case "$first" in v1.1.1-dirty.*) ;; *) exit 1 ;; esac
  record_deployed_version "$first"
  second="$(resolve_image_tag)"
  case "$second" in v1.1.2-dirty.*) ;; *) exit 1 ;; esac
' _ "$ROOT"

echo "default deployment flow checks passed"
