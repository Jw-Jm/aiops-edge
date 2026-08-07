#!/usr/bin/env bash
# =============================================================================
# 一键部署 AIOps 平台到本机 K8s (OrbStack)
# 1) 部署 observability（4 自研服务 + 8 中间件）
# 2) 部署 deepflow 到独立 deepflow namespace（完整装）
# 用法: ./apply.sh
# =============================================================================
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/../helm/aiops" && pwd)"

# 确保 deepflow chart 仓库已添加
helm repo add deepflow https://deepflowio.github.io/deepflow >/dev/null 2>&1 || true
helm repo update deepflow >/dev/null 2>&1 || true

echo "=== [1/2] 部署 observability (自研 + 中间件) ==="
# 本机开发默认密钥（生产环境务必用 values-prod.yaml 覆盖真实密钥）
helm upgrade --install aiops "$CHART_DIR" \
  --namespace observability --create-namespace \
  --set deepflow.enabled=false \
  --set secrets.jwtSecret="dev-jwt-secret-change-me" \
  --set secrets.internalToken="dev-internal-token" \
  --set secrets.ingestApiKey="dev-ingest-key" \
  --set secrets.clickhousePassword="dev-ch-pass" \
  --set secrets.redisPassword="dev-redis-pass" \
  --set secrets.minioAccessKey="minioadmin" \
  --set secrets.minioSecretKey="minioadmin123" \
  --set secrets.mysqlRootPassword="dev-mysql-pass" \
  --wait \
  --timeout 15m

echo "=== [2/2] 部署 deepflow (独立 deepflow namespace, 完整装) ==="
helm upgrade --install deepflow deepflow/deepflow \
  --version 7.1.002 \
  --namespace deepflow --create-namespace \
  --values "$CHART_DIR/values-deepflow.yaml" \
  --wait \
  --timeout 15m || {
    echo "警告: deepflow 部署未完成(可能因资源/网络)，可稍后单独重试:"
    echo "  helm upgrade --install deepflow deepflow/deepflow -n deepflow -f $CHART_DIR/values-deepflow.yaml"
  }

echo ""
echo "部署命令完成。访问: http://localhost:30253"
echo "验证: kubectl -n observability get pods && kubectl -n deepflow get pods"
