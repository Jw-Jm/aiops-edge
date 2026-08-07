#!/usr/bin/env bash
# =============================================================================
# 卸载 AIOps 平台（默认保留 PVC；--purge-data 才删除 PVC 与 namespace）
# 用法: ./destroy.sh [--purge-data]
# =============================================================================
set -euo pipefail

helm uninstall aiops --namespace observability 2>/dev/null || echo "observability release 不存在，跳过"
helm uninstall deepflow --namespace deepflow 2>/dev/null || echo "deepflow release 不存在，跳过"

if [[ "${1:-}" == "--purge-data" ]]; then
  echo ">>> 删除 namespace (含 PVC 数据)..."
  kubectl delete ns observability deepflow --wait 2>/dev/null || true
  echo "已清除全部数据。"
else
  echo "已卸载 release（PVC 保留）。如需连数据一起清除: ./destroy.sh --purge-data"
fi
