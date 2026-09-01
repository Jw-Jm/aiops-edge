#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="${repo_root}/deploy/scripts/graph-capacity-gate.sh"

[[ -x "${script}" ]] || {
  echo "graph capacity gate contract failed: gate is missing or not executable" >&2
  exit 1
}

for required in \
  'GRAPH_CAPACITY_VERTICES:-200000' \
  'GRAPH_CAPACITY_EDGES:-1000000' \
  '--batch-benchmark-iterations 0' \
  '--project-query-aliases' \
  'pressure_test' \
  'request_once health' \
  'request_once entity' \
  'request_once alias_search' \
  'request_once neighbors' \
  'request_once candidate' \
  'request_once impact' \
  'request_once path'; do
  rg -n --fixed-strings -- "${required}" "${script}" >/dev/null || {
    echo "graph capacity gate contract failed: missing ${required}" >&2
    exit 1
  }
done

if rg -n --fixed-strings -- '--batch-benchmark-iterations "$benchmark_iterations"' "${script}" >/dev/null; then
  echo "graph capacity gate contract failed: benchmark iterations are not fixed to zero" >&2
  exit 1
fi

echo "graph capacity gate contract tests passed"
