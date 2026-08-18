#!/usr/bin/env bash
# =============================================================================
# 构建全部自研服务镜像。
# 可移植性：registry 前缀 / tag / 平台 均可通过环境变量注入，不写死本环境。
#   用法: ./build-images.sh [service]
#   环境变量:
#     IMAGE_REGISTRY  镜像仓库前缀（如 registry.example.com/aiops，默认空=本地）
#     IMAGE_TAG       镜像标签（默认 latest）
#     BUILD_PLATFORM  构建平台（默认当前架构；跨架构如 linux/amd64 可覆盖）
# =============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/version.sh"

REGISTRY="${IMAGE_REGISTRY:-}"
# 镜像 tag 与 Chart.yaml 的 appVersion 对齐。
# 重要：TAG 必须与 values.yaml 的 global.imageTag 一致——CI 中应注入相同的 IMAGE_TAG，
#       否则 build 出的 tag 与 Helm 期望不一致，本地已有旧镜像时会静默部署旧版本（P0-1 事故根因）。
# 默认版本由 version.sh 根据 Chart.yaml 和本地部署计数生成；手工发布可用 IMAGE_TAG 覆盖。
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
  local dir="$1" name="$2"
  local full
  full="$(prefix "$name"):${TAG}"
  echo ">>> building $full from $dir"
  local platform_args=()
  if [ -n "$PLATFORM" ]; then
    (cd "$ROOT/$dir" && docker build --platform "$PLATFORM" -t "$full" .)
  else
    (cd "$ROOT/$dir" && docker build -t "$full" .)
  fi
  docker image inspect "$full" >/dev/null
  echo ">>> built $full"
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
    build ipmi-exporter ipmi-exporter
    ;;
  frontend)  build observability-frontend observability-frontend ;;
  query-api) build ai-apm-query-go query-api ;;
  ingest)    build ai-apm-ingest-go ingest-pipeline ;;
  orchestrator) build ai-orchestrator ai-orchestrator ;;
  event-collector) build ai-event-collector event-collector ;;
  ipmi)      build ipmi-exporter ipmi-exporter ;;
  *)
    echo "未知服务: $TARGET (可选: all/frontend/query-api/ingest/orchestrator/event-collector/ipmi)"
    exit 1
    ;;
esac

echo "镜像构建完成。"
