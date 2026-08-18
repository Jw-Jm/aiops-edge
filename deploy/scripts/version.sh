#!/usr/bin/env bash
# Shared local release-version helpers for the build/deploy scripts.

set -euo pipefail

: "${ROOT:?ROOT must point at the project root before sourcing version.sh}"

VERSION_FILE="${AIOPS_VERSION_FILE:-$ROOT/.deploy-version}"
CHART_FILE="$ROOT/deploy/helm/aiops/Chart.yaml"

chart_app_version() {
  awk -F'"' '/^[[:space:]]*appVersion:[[:space:]]*"/ { print $2; exit }' "$CHART_FILE"
}

bump_patch() {
  local version="$1"
  if [[ "$version" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
    patch="${BASH_REMATCH[3]}"
    printf '%s.%s.%s\n' "$major" "$minor" "$((patch + 1))"
  else
    echo "invalid release version: $version" >&2
    return 1
  fi
}

tracked_tree_dirty() {
  ! git -C "$ROOT" diff --quiet HEAD --
}

resolve_image_tag() {
  if [[ -n "${IMAGE_TAG:-}" ]]; then
    printf '%s\n' "$IMAGE_TAG"
    return 0
  fi

  local version
  if [[ -s "$VERSION_FILE" ]]; then
    version="$(bump_patch "$(<"$VERSION_FILE")")"
  else
    version="$(chart_app_version)"
  fi

  local tag="v${version}"
  if tracked_tree_dirty; then
    tag="${tag}-dirty.$(date -u +%Y%m%d%H%M%S)"
  fi
  printf '%s\n' "$tag"
}

record_deployed_version() {
  local tag="$1"
  if [[ "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)(-.+)?$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}" > "$VERSION_FILE"
  else
    echo "not recording non-semver image tag: $tag" >&2
  fi
}
