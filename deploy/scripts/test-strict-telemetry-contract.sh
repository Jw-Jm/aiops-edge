#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOADGEN_DOCKERFILE="$ROOT_DIR/ai-apm-ingest-go/Dockerfile"
PREPARE="$ROOT_DIR/deploy/scripts/prepare-strict-manual-test.sh"

test -x "$PREPARE"
rg -q 'strict' "$PREPARE"
rg -q '/loadgen' "$PREPARE"
rg -q 'kubectl exec.*loadgen' "$PREPARE"
rg -q 'dashboard/stats|services/map|traces' "$PREPARE"
rg -q '120' "$PREPARE"
! rg -q 'INSERT|UPDATE|clickhouse-client|victoria' "$PREPARE"
rg -q 'go build .*tools/loadgen|/loadgen' "$LOADGEN_DOCKERFILE"
! rg -q 'loadgen.*(Deployment|CronJob)|Deployment.*loadgen' "$ROOT_DIR/deploy/helm/aiops"
if rg -n '(echo|printf|logger|log\.).*(API_KEY|TOKEN|PASSWORD|SECRET)' "$PREPARE"; then
  echo 'prepare script appears to log a secret' >&2
  exit 1
fi
echo 'strict telemetry contract: PASS'
