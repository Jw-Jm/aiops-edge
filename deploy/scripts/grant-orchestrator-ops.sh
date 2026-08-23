#!/usr/bin/env bash
# 临时授予 orchestrator K8s 写权限（仅 observability 命名空间 deployments patch）。
# 执行演练后必须调用 revoke-orchestrator-ops.sh 撤销，回到 fail-closed 默认。
set -euo pipefail
RELEASE="${RELEASE:-aiops}"
CHART="${CHART:-./deploy/helm/aiops}"
NS="${NS:-observability}"
kubectl config use-context orbstack
helm upgrade "${RELEASE}" "${CHART}" -n "${NS}" --reuse-values \
  --set aiOrchestrator.grantK8sWrite=true
kubectl rollout restart deployment/ai-orchestrator -n observability
echo "[grant] orchestrator K8s write ENABLED (grantK8sWrite=true). Revoke after drill."
