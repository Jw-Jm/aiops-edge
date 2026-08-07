#!/usr/bin/env bash
# =============================================================================
# 构建 4 个自研服务镜像（arm64，本机 OrbStack Docker）
# 用法: ./build-images.sh [service]   # 省略则全量构建
# =============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

build() {
  local dir="$1" name="$2"
  echo ">>> building $name from $dir"
  # 用本地 tag（无 registry 前缀），OrbStack K8s 直接使用本地镜像
  (cd "$ROOT/$dir" && docker build --platform linux/arm64 -t "$name:latest" .)
  echo ">>> built $name"
}

# 指定服务则只构建该服务
TARGET="${1:-all}"
case "$TARGET" in
  all)
    build observability-frontend observability-frontend
    build ai-apm-query-go query-api
    build ai-apm-ingest-go ingest-pipeline
    build ai-orchestrator ai-orchestrator
    ;;
  frontend)  build observability-frontend observability-frontend ;;
  query-api) build ai-apm-query-go query-api ;;
  ingest)    build ai-apm-ingest-go ingest-pipeline ;;
  orchestrator) build ai-orchestrator ai-orchestrator ;;
  *)
    echo "未知服务: $TARGET (可选: all/frontend/query-api/ingest/orchestrator)"
    exit 1
    ;;
esac

echo "镜像构建完成。"
