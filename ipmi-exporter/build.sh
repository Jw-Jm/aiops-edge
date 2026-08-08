#!/usr/bin/env bash
# ipmi-exporter 镜像构建脚本
# 用法: ./build.sh [tag]
set -euo pipefail

TAG="${1:-latest}"
IMAGE="aiops/ipmi-exporter:${TAG}"

echo ">>> Building ${IMAGE} ..."
docker build -t "${IMAGE}" -f Dockerfile .

echo ">>> Done: ${IMAGE}"
echo ">>> 推送（如需）: docker push ${IMAGE}"
