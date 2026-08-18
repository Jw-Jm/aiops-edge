#!/usr/bin/env bash
# =============================================================================
# 一键部署 AIOps 平台到本机 K8s (OrbStack)
# 1) 构建并部署 observability（5 自研服务 + 8 中间件）
# 2) 部署 deepflow 到独立 deepflow namespace（完整装）
# 用法: ./apply.sh
# 默认会从当前项目源码构建带版本号的本地镜像；仅在明确设置 SKIP_IMAGE_BUILD=1 时跳过构建。
# =============================================================================
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/../helm/aiops" && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/version.sh"

# 确保 deepflow chart 仓库已添加
helm repo add deepflow https://deepflowio.github.io/deepflow >/dev/null 2>&1 || true
helm repo update deepflow >/dev/null 2>&1 || true

echo "=== [1/2] 部署 observability (自研 + 中间件) ==="
# =============================================================================
# 凭据注入（G5 安全加固）
# 生产凭据必须由环境变量注入，禁止在脚本/仓库中提交硬编码默认值。
# 部署前必须设置以下环境变量（缺失即报错退出，绝不降级为弱默认值）：
#   ADMIN_PASSWORD         admin 初始密码
#   CLICKHOUSE_PASSWORD    ClickHouse default 用户密码
#   MYSQL_ROOT_PASSWORD    MySQL root 密码
#   INTERNAL_TOKEN         orchestrator 内部调用 token
#   INGEST_API_KEY         ingest 数据接收 API key
# 可选覆盖（不设置则复用集群中已有 Secret 或生成随机值）：
#   JWT_SECRET / LLM_ENCRYPTION_KEY（>=32 字符，query-api 启动强校验）
# =============================================================================
require_env() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    echo "错误: 环境变量 $name 未设置。" >&2
    echo "生产部署必须显式注入凭据，禁止使用硬编码/弱默认值。" >&2
    echo "请设置后重试，例如:" >&2
    echo "  export ADMIN_PASSWORD='<强随机值>'" >&2
    echo "  export CLICKHOUSE_PASSWORD='<强随机值>'" >&2
    echo "  export MYSQL_ROOT_PASSWORD='<强随机值>'" >&2
    echo "  export INTERNAL_TOKEN='<强随机值>'" >&2
    echo "  export INGEST_API_KEY='<强随机值>'" >&2
    exit 1
  fi
}
require_env ADMIN_PASSWORD
require_env CLICKHOUSE_PASSWORD
require_env MYSQL_ROOT_PASSWORD
require_env INTERNAL_TOKEN
require_env INGEST_API_KEY

IMAGE_TAG_VAL="$(resolve_image_tag)"
if [[ "${SKIP_IMAGE_BUILD:-0}" != "1" ]]; then
  echo "=== [0/2] 构建项目最新镜像 ($IMAGE_TAG_VAL) ==="
  IMAGE_TAG="$IMAGE_TAG_VAL" "$SCRIPT_DIR/build-images.sh" all
else
  echo "=== [0/2] 跳过镜像构建 ($IMAGE_TAG_VAL) ==="
fi

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
JWT_SECRET_VAL="${JWT_SECRET:-$(get_or_gen aiops-secrets JWT_SECRET dev-jwt-)}"
LLM_KEY_VAL="${LLM_ENCRYPTION_KEY:-$(get_or_gen aiops-secrets LLM_ENCRYPTION_KEY dev-llm-)}"
helm upgrade --install aiops "$CHART_DIR" \
  --namespace observability --create-namespace \
  --set deepflow.enabled=false \
  --set global.imageTag="${IMAGE_TAG_VAL}" \
  --set secrets.jwtSecret="${JWT_SECRET_VAL}" \
  --set secrets.llmEncryptionKey="${LLM_KEY_VAL}" \
  --set secrets.internalToken="${INTERNAL_TOKEN}" \
  --set secrets.ingestApiKey="${INGEST_API_KEY}" \
  --set secrets.adminInitialPassword="${ADMIN_PASSWORD}" \
  --set secrets.clickhousePassword="${CLICKHOUSE_PASSWORD}" \
  --set secrets.mysqlRootPassword="${MYSQL_ROOT_PASSWORD}" \
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
record_deployed_version "$IMAGE_TAG_VAL"
echo "部署命令完成。镜像版本: ${IMAGE_TAG_VAL}。访问: http://localhost:30253"
echo "验证: kubectl -n observability get pods && kubectl -n deepflow get pods"
