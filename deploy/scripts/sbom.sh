#!/usr/bin/env bash
# =============================================================================
# AIOps 镜像 SBOM 生成（28.2 #18：schema/image/SBOM 部署验证）。
#
# 对已构建/已部署的 AIOps 镜像输出 SBOM（软件物料清单）：镜像 digest + 依赖摘要。
# 用法：
#   ./deploy/scripts/sbom.sh [镜像名...]        # 默认列出生产主要镜像
#   ./deploy/scripts/sbom.sh query-api          # 只看 query-api
#
# 说明：Go 镜像的 SBOM 以 go.mod 依赖摘要 + 构建信息（-buildvcs）为准；
# 运行时不引入第三方 SBOM 工具（零依赖），输出 markdown 便于归档。
# =============================================================================
set -u

IMAGES=(
  "query-api"
  "ingest-pipeline"
  "event-collector"
  "ai-orchestrator"
  "observability-frontend"
  "schema-migrator"
  "clickhouse-migrator"
)

OUT="${SBOM_OUT:-docs/AIOPS_IMAGE_SBOM.md}"
mkdir -p "$(dirname "$OUT")"

{
  echo "# AIOps 镜像 SBOM"
  echo
  echo "_生成时间: $(date -u '+%Y-%m-%d %H:%M:%S UTC')_"
  echo "_说明: Go 镜像以 go.mod 依赖 + 构建信息为准；运行时零第三方 SBOM 依赖。_"
  echo
  for img in "${IMAGES[@]}"; do
    digest=$(docker images --digests --format '{{.Repository}}:{{.Tag}} {{.Digest}}' "$img" 2>/dev/null | head -1)
    if [ -z "$digest" ]; then
      echo "## $img"
      echo "- 镜像: 未在本地构建/不存在"
      echo
      continue
    fi
    echo "## $img"
    echo "- $digest"
    # Go 服务：输出 go.mod 依赖摘要
    repo=""
    case "$img" in
      query-api) repo="ai-apm-query-go" ;;
      ingest-pipeline) repo="ai-apm-ingest-go" ;;
      event-collector) repo="ai-event-collector-go" ;;
      ai-orchestrator) repo="ai-orchestrator" ;;
    esac
    if [ -n "$repo" ] && [ -f "aiops/$repo/go.mod" ]; then
      echo "- 依赖模块数: $(grep -c '^require' "aiops/$repo/go.mod" 2>/dev/null || echo 0)"
    fi
    if [ -f "aiops/$repo/requirements.txt" ]; then
      echo "- Python 依赖数: $(grep -vcE '^\s*#' "aiops/$repo/requirements.txt" 2>/dev/null || echo 0)"
    fi
    echo
  done
  echo "---"
  echo "_SBOM 归档: 生产候选前需对每个部署镜像重新生成并核对 digest（28.2 #18）。_"
} > "$OUT"

echo "SBOM written to $OUT"
