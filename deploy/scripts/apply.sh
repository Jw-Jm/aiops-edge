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
# 本机开发默认密钥（生产环境务必用 values-prod.yaml 覆盖真实密钥）。
# 说明：jwtSecret / llmEncryptionKey 必须 >=32 字符（query-api 启动强校验，缺省会拒绝启动）。
# P0-1 修复(密钥漂移)：若集群中已存在 aiops-secrets（含 LLM_ENCRYPTION_KEY），则复用其值，
# 不再每次 helm upgrade 随机生成 —— 否则密钥轮换会导致已保存的 LLM API Key 密文无法解密，
# 全站 AI 静默降级为确定性模式（此前实测即此根因）。
# 首次部署时生成固定 32+ 字符的开发密钥并持久化到 Secret，后续升级复用。
get_or_gen() {
  local secret_name="$1" key_name="$2" prefix="$3"
  local existing
  existing="$(kubectl get secret "$secret_name" -n observability -o jsonpath="{.data.$key_name}" 2>/dev/null | base64 -d 2>/dev/null || true)"
  if [ -n "$existing" ]; then
    echo "$existing"
  else
    printf '%s%.32s' "$prefix" "$(openssl rand -hex 32 2>/dev/null || echo 0123456789abcdef0123456789abcdef)"
  fi
}
DEV_JWT="$(get_or_gen aiops-secrets JWT_SECRET dev-jwt-)"
DEV_LLM_KEY="$(get_or_gen aiops-secrets LLM_ENCRYPTION_KEY dev-llm-)"
DEV_MINIO_SECRET="$(get_or_gen aiops-secrets MINIO_SECRET_KEY devminio)"
helm upgrade --install aiops "$CHART_DIR" \
  --namespace observability --create-namespace \
  --set deepflow.enabled=false \
  --set secrets.jwtSecret="${DEV_JWT}" \
  --set secrets.llmEncryptionKey="${DEV_LLM_KEY}" \
  --set secrets.internalToken="dev-internal-token" \
  --set secrets.ingestApiKey="dev-ingest-key" \
  --set secrets.clickhousePassword="dev-ch-pass" \
  --set secrets.redisPassword="dev-redis-pass" \
  --set secrets.minioAccessKey="aiopsdev" \
  --set secrets.minioSecretKey="${DEV_MINIO_SECRET}" \
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
