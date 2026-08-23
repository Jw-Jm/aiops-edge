#!/usr/bin/env bash
# 撤销临时写权限，回到 fail-closed 默认（grantK8sWrite=false）。
set -euo pipefail
RELEASE="${RELEASE:-aiops}"
CHART="${CHART:-./deploy/helm/aiops}"
NS="${NS:-aiops-system}"
kubectl config use-context orbstack
helm upgrade "${RELEASE}" "${CHART}" -n "${NS}" --reuse-values \
  --set aiOrchestrator.grantK8sWrite=false
kubectl rollout restart deployment/ai-orchestrator -n observability
echo "[revoke] orchestrator K8s write DISABLED (grantK8sWrite=false)."
