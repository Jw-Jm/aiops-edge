#!/usr/bin/env bash
# =============================================================================
# 一键部署 AIOps 平台到本机 K8s (OrbStack)
# 1) 构建并部署 observability（5 自研服务 + 8 中间件）
# 2) 部署 deepflow 到独立 deepflow namespace（完整装）
# 用法: ./apply.sh
# 默认会从当前项目源码构建带版本号的本地镜像；仅在明确设置 SKIP_IMAGE_BUILD=1 时跳过构建。
# Fresh Install 验证请使用 `FRESH_INSTALL_VALIDATION=1 ./apply.sh`，该路径委托给
# local-validation.sh 的 bootstrap → schema-migrator → runtime 两阶段流程。
# =============================================================================
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/../helm/aiops" && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/version.sh"

if [[ "${FRESH_INSTALL_VALIDATION:-0}" == "1" ]]; then
  exec "${SCRIPT_DIR}/local-validation.sh" "$@"
fi

# =============================================================================
# 生产环境守卫（P1-S3）
# AIOPS_ENV=production 时，本脚本的本地联调默认值（弱初始密码 / 关闭首次改密 /
# dev- 前缀 JWT）一律禁止：要么显式提供生产值，要么拒绝部署。fail-closed。
# =============================================================================
is_production() {
  [[ "$(printf '%s' "${AIOPS_ENV:-}" | tr '[:upper:]' '[:lower:]')" == "production" ]]
}
if is_production; then
  if [[ "${ADMIN_PASSWORD:-}" == "" || "${ADMIN_PASSWORD:-}" == "admin1234" ]]; then
    echo "错误: AIOPS_ENV=production 禁止缺省/弱 ADMIN_PASSWORD，请设置强随机值。" >&2
    exit 1
  fi
  if [[ "${JWT_SECRET:-}" != "" && "${JWT_SECRET}" == dev-jwt-* ]]; then
    echo "错误: AIOPS_ENV=production 禁止 dev- 前缀的 JWT_SECRET。" >&2
    exit 1
  fi
fi

# 确保 deepflow chart 仓库已添加
helm repo add deepflow https://deepflowio.github.io/deepflow >/dev/null 2>&1 || true
helm repo update deepflow >/dev/null 2>&1 || true

# =============================================================================
# 凭据注入（G5 安全加固）
# 生产首次部署的凭据必须由环境变量注入，禁止在脚本/仓库中提交硬编码默认值。
# 若集群中已有 aiops-secrets，后续升级会优先复用已有值，避免 StatefulSet 数据密码漂移。
# 首次部署且 Secret 不存在时必须设置以下环境变量：
#   ADMIN_PASSWORD         admin 初始密码
#   CLICKHOUSE_PASSWORD    ClickHouse default 用户密码
#   MYSQL_ROOT_PASSWORD    MySQL root 密码
#   INTERNAL_TOKEN         orchestrator 内部调用 token
#   INGEST_API_KEY         ingest 数据接收 API key
# 可选覆盖（不设置则复用集群中已有 Secret 或生成随机值）：
#   JWT_SECRET / LLM_ENCRYPTION_KEY（>=32 字符，query-api 启动强校验）
# =============================================================================
secret_value() {
  local key="$1"
  kubectl get secret aiops-secrets -n observability -o jsonpath="{.data.${key}}" 2>/dev/null \
    | base64 -d 2>/dev/null || true
}

resolve_required_secret() {
  local env_name="$1" secret_key="$2" value
  value="${!env_name:-}"
  if [ -n "$value" ]; then
    printf '%s' "$value"
    return 0
  fi
  value="$(secret_value "$secret_key")"
  if [ -n "$value" ]; then
    printf '%s' "$value"
    return 0
  fi
  echo "错误: 首次部署必须设置环境变量 $env_name。" >&2
  echo "请设置强随机值后重试；后续升级会自动复用集群 Secret。" >&2
  return 1
}

# Local first bootstrap has a deterministic credential. An explicit
# ADMIN_PASSWORD or an
# existing Secret still wins and is never overwritten by an upgrade.
# 生产模式已在上方守卫中强制显式 ADMIN_PASSWORD，缺省 admin1234 仅限本地联调。
ADMIN_PASSWORD_VAL="${ADMIN_PASSWORD:-$(secret_value ADMIN_INITIAL_PASSWORD)}"
if [ -z "$ADMIN_PASSWORD_VAL" ]; then
  ADMIN_PASSWORD_VAL="admin1234"
fi
CLICKHOUSE_PASSWORD_VAL="$(resolve_required_secret CLICKHOUSE_PASSWORD CLICKHOUSE_PASSWORD)"
MYSQL_ROOT_PASSWORD_VAL="$(resolve_required_secret MYSQL_ROOT_PASSWORD MYSQL_ROOT_PASSWORD)"
INTERNAL_TOKEN_VAL="$(resolve_required_secret INTERNAL_TOKEN INTERNAL_TOKEN)"
INGEST_API_KEY_VAL="$(resolve_required_secret INGEST_API_KEY INGEST_API_KEY)"

IMAGE_TAG_VAL="$(resolve_image_tag)"
if [[ "${SKIP_IMAGE_BUILD:-0}" != "1" ]]; then
  echo "=== [0/2] 构建项目最新镜像 ($IMAGE_TAG_VAL) ==="
  IMAGE_TAG="$IMAGE_TAG_VAL" "$SCRIPT_DIR/build-images.sh" all
else
  echo "=== [0/2] 跳过镜像构建 ($IMAGE_TAG_VAL) ==="
fi

echo "=== [1/2] 部署 observability (自研 + 中间件) ==="

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
# 生产模式下禁止关闭首次登录强制改密（chart 默认 true 承担强制；本地联调才显式关闭）
AUTH_PWD_CHANGE_ARGS=(--set queryApi.authRequireFirstLoginPasswordChange=false)
if is_production; then
  AUTH_PWD_CHANGE_ARGS=()
fi
# helm 密钥参数：构造为 argv 数组再整体传给 helm，避免逐一内联展开。
# 注意：secrets 仍会留存于 helm release 值；生产应优先使用 values-prod.yaml
# 的 existingSecretName 外部 Secret 模式（chart 已支持）。
HELM_SET_ARGS=(
  --set-string "secrets.jwtSecret=${JWT_SECRET_VAL}"
  --set-string "secrets.llmEncryptionKey=${LLM_KEY_VAL}"
  --set-string "secrets.internalToken=${INTERNAL_TOKEN_VAL}"
  --set-string "secrets.ingestApiKey=${INGEST_API_KEY_VAL}"
  --set-string "secrets.adminInitialPassword=${ADMIN_PASSWORD_VAL}"
  --set-string "secrets.clickhousePassword=${CLICKHOUSE_PASSWORD_VAL}"
  --set-string "secrets.mysqlRootPassword=${MYSQL_ROOT_PASSWORD_VAL}"
)
helm upgrade --install aiops "$CHART_DIR" \
  --namespace observability --create-namespace \
  --set deepflow.enabled=false \
  ${AUTH_PWD_CHANGE_ARGS[@]+"${AUTH_PWD_CHANGE_ARGS[@]}"} \
  --set global.imageTag="${IMAGE_TAG_VAL}" \
  "${HELM_SET_ARGS[@]}" \
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
