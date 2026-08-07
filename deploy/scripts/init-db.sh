#!/usr/bin/env bash
# =============================================================================
# 手动建表逃生门（正常由 helm post-install hook Job 自动建表；应急/排障用）
# 用法: ./init-db.sh [namespace]   # 默认 observability
# =============================================================================
set -euo pipefail

NS="${1:-observability}"
SQL_FILE="$(cd "$(dirname "$0")/../helm/aiops/files/clickhouse" && pwd)/init_clickhouse.sql"

echo ">>> 等待 clickhouse-0 就绪..."
until kubectl -n "$NS" exec clickhouse-0 -- clickhouse-client --query 'SELECT 1' >/dev/null 2>&1; do
  sleep 3
done

echo ">>> 手动执行建表: $SQL_FILE"
kubectl -n "$NS" exec -i clickhouse-0 -- clickhouse-client --multiquery < "$SQL_FILE"
echo ">>> 建表完成。"

echo ""
echo "验证表:"
kubectl -n "$NS" exec clickhouse-0 -- clickhouse-client --query "SELECT name FROM system.tables WHERE database='observability' ORDER BY name"
