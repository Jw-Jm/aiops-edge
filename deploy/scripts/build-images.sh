#!/usr/bin/env bash
# =============================================================================
# 构建全部自研服务镜像。
# 可移植性：registry 前缀 / tag / 平台 均可通过环境变量注入，不写死本环境。
#   用法: ./build-images.sh [service]
#   环境变量:
#     IMAGE_REGISTRY  镜像仓库前缀（如 registry.example.com/aiops，默认空=本地）
#     IMAGE_TAG       镜像标签（默认 git-<当前 SHA>）
#     BUILD_PLATFORM  构建平台（默认当前架构；跨架构如 linux/amd64 可覆盖）
#     BUILD_IMAGES_DRY_RUN=1 只打印构建清单，不连接 Docker daemon
# =============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/version.sh"

REGISTRY="${IMAGE_REGISTRY:-}"
# 镜像 tag 与 Chart.yaml 的 appVersion 对齐。
# 重要：TAG 必须与 values.yaml 的 global.imageTag 一致——CI 中应注入相同的 IMAGE_TAG，
#       否则 build 出的 tag 与 Helm 期望不一致，本地已有旧镜像时会静默部署旧版本（P0-1 事故根因）。
# 默认版本由 version.sh 根据当前 Git SHA 生成；手工发布可用 IMAGE_TAG/RELEASE_TAG 覆盖。
TAG="${IMAGE_TAG:-$(resolve_image_tag)}"
PLATFORM="${BUILD_PLATFORM:-}"

# 本地镜像时（无 registry）不加前缀，K8s 直接用本地镜像；有 registry 时拼前缀
prefix() {
  if [ -n "$REGISTRY" ]; then
    echo "${REGISTRY}/${1}"
  else
    echo "$1"
  fi
}

build() {
  local dir="$1" name="$2" dockerfile="${3:-Dockerfile}"
  local full
  full="$(prefix "$name"):${TAG}"
  echo ">>> building $full from $dir"
  if [[ "${BUILD_IMAGES_DRY_RUN:-0}" == "1" ]]; then
    if [[ "$dockerfile" != "Dockerfile" ]]; then
      echo "docker build ${PLATFORM:+--platform ${PLATFORM}} -f ${dockerfile} -t ${full} ${ROOT}/${dir}"
    else
      echo "docker build ${PLATFORM:+--platform ${PLATFORM}} -t ${full} ${ROOT}/${dir}"
    fi
    return 0
  fi
  if [[ "${TAG}" == "latest" ]]; then
    echo "refusing mutable latest image tag" >&2
    exit 1
  fi
  if [ -n "$PLATFORM" ]; then
    (cd "$ROOT/$dir" && docker build --platform "$PLATFORM" -f "$dockerfile" -t "$full" .)
  else
    (cd "$ROOT/$dir" && docker build -f "$dockerfile" -t "$full" .)
  fi
  docker image inspect "$full" >/dev/null
  echo ">>> built $full"
}

build_clickhouse_migrator() {
  local full
  full="$(prefix clickhouse-migrator):${TAG}"
  echo ">>> building ${full} from deploy/tools/clickhouse-migrator"
  if [[ "${BUILD_IMAGES_DRY_RUN:-0}" == "1" ]]; then
    echo "docker build ${PLATFORM:+--platform ${PLATFORM}} -t ${full} ${ROOT}/deploy/tools/clickhouse-migrator"
    return 0
  fi
  if [[ "${TAG}" == "latest" ]]; then
    echo "refusing mutable latest image tag" >&2
    exit 1
  fi
  if [ -n "${PLATFORM}" ]; then
    docker build --platform "${PLATFORM}" -t "${full}" "${ROOT}/deploy/tools/clickhouse-migrator"
  else
    docker build -t "${full}" "${ROOT}/deploy/tools/clickhouse-migrator"
  fi
  docker image inspect "${full}" >/dev/null
  echo ">>> built ${full}"
}

# 指定服务则只构建该服务
TARGET="${1:-all}"
case "$TARGET" in
  all)
    build observability-frontend observability-frontend
    build ai-apm-query-go query-api
    build ai-apm-ingest-go ingest-pipeline
    build ai-orchestrator ai-orchestrator
    build ai-event-collector event-collector
    build ai-action-executor ai-action-executor
    build ai-credential-broker ai-credential-broker
    build ai-llm-egress-proxy ai-llm-egress-proxy
    build ai-apm-query-go schema-migrator Dockerfile.schema-migrator
    build ai-apm-query-go graph-schema-migrator Dockerfile.graph-schema-migrator
    build_clickhouse_migrator
    build ipmi-exporter ipmi-exporter
    ;;
  frontend)  build observability-frontend observability-frontend ;;
  query-api) build ai-apm-query-go query-api ;;
  ingest)    build ai-apm-ingest-go ingest-pipeline ;;
  orchestrator) build ai-orchestrator ai-orchestrator ;;
  event-collector) build ai-event-collector event-collector ;;
  executor)  build ai-action-executor ai-action-executor ;;
  credential-broker) build ai-credential-broker ai-credential-broker ;;
  llm-proxy) build ai-llm-egress-proxy ai-llm-egress-proxy ;;
  schema-migrator) build ai-apm-query-go schema-migrator Dockerfile.schema-migrator ;;
  graph-schema-migrator) build ai-apm-query-go graph-schema-migrator Dockerfile.graph-schema-migrator ;;
  clickhouse-migrator) build_clickhouse_migrator ;;
  ipmi)      build ipmi-exporter ipmi-exporter ;;
  *)
    echo "未知服务: $TARGET (可选: all/frontend/query-api/ingest/orchestrator/event-collector/executor/credential-broker/llm-proxy/schema-migrator/graph-schema-migrator/clickhouse-migrator/ipmi)"
    exit 1
    ;;
esac

echo "镜像构建完成。"
