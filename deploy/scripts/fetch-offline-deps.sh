#!/usr/bin/env bash
# =============================================================================
# 离线依赖预下载脚本（国内源）— 一次性把构建所需依赖下载到项目目录缓存，
# 后续镜像构建从本地缓存拉取，避免重复下载、节约构建时间。
#
# 用法: ./fetch-offline-deps.sh
# 覆盖:
#   1. query-api  Go 模块 → go mod vendor（已入库，零网络构建）
#   2. frontend   npm 包   → 预装 node_modules 到 deploy/offline/frontend-node_modules
#   3. orchestrator Python 依赖 → 已离线（bin/sp.tar.gz 容器导出包，无需下载）
#   4. kubectl/k8sgpt 二进制 → 已本地化（query-api/docker、orchestrator/bin）
#
# 国内源: npm=npmmirror | go=goproxy.cn | apk=阿里云 | apt=清华（Dockerfile 内已配）
# =============================================================================
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OFFLINE_DIR="$ROOT/deploy/offline"
FRONTEND_DIR="$ROOT/observability-frontend"

mkdir -p "$OFFLINE_DIR"

echo ">>> [1/2] frontend: npm ci 预装到 $OFFLINE_DIR/frontend-node_modules"
if [ -d "$OFFLINE_DIR/frontend-node_modules" ] && [ -f "$OFFLINE_DIR/frontend-node_modules/.offline-ready" ]; then
  echo "    缓存已存在，跳过（如需刷新删除 $OFFLINE_DIR/frontend-node_modules 后重跑）"
else
  (
    cd "$FRONTEND_DIR"
    npm config set registry https://registry.npmmirror.com
    npm ci 2>/dev/null || npm install
  )
  rm -rf "$OFFLINE_DIR/frontend-node_modules"
  cp -R "$FRONTEND_DIR/node_modules" "$OFFLINE_DIR/frontend-node_modules"
  touch "$OFFLINE_DIR/frontend-node_modules/.offline-ready"
  echo "    已缓存 $(du -sh "$OFFLINE_DIR/frontend-node_modules" | cut -f1)"
fi

echo ">>> [2/2] query-api: go mod vendor（依赖已入库，验证完整性）"
(
  cd "$ROOT/ai-apm-query-go"
  GOPROXY=https://goproxy.cn,direct go mod vendor
)
echo "    vendor 就绪: $(du -sh "$ROOT/ai-apm-query-go/vendor" | cut -f1)"

echo ">>> 全部离线依赖就绪。"
echo "    说明: orchestrator Python 依赖已用 bin/sp.tar.gz 离线包（容器导出），"
echo "          kubectl/k8sgpt 二进制已本地化，无需额外下载。"
