#!/usr/bin/env bash
# =============================================================================
# 多集群采集器推广演示触发脚本（模拟数据，可选工具）
# 用途: 在本地/开发环境跑一次 multicluster_demo.py，向 ClickHouse 写入
#       模拟集群 cluster-b / cluster-c 的 trace + service_topology，
#       注册 MySQL clusters，触发图谱构建并输出验证摘要。
# 前提: 能连上集群（kubectl），本机 8123(ClickHouse)/3306(MySQL) 可访问。
#       - 若本机已存在端口转发（kubectl port-forward），直接运行即可
#       - 否则脚本自动做一次性 kubectl port-forward 并在结束前杀掉
# 用法: ./multicluster-demo.sh [--traces 200] [--clear]
#       ./multicluster-demo.sh --help   # 查看 demo 全部参数
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ORCH_DIR="$(cd "$HERE/../../ai-orchestrator" && pwd)"
NS="${KUBE_NS:-observability}"

# 从 k8s Secret 提取 CH/MySQL 口令（本地端口转发场景使用）
CH_PASS=""
MYSQL_PASS=""
if command -v kubectl >/dev/null 2>&1 && kubectl get secret aiops-secrets -n "$NS" >/dev/null 2>&1; then
  CH_PASS="$(kubectl -n "$NS" get secret aiops-secrets -o jsonpath='{.data.CLICKHOUSE_PASSWORD}' | base64 -d 2>/dev/null || true)"
  MYSQL_PASS="$(kubectl -n "$NS" get secret aiops-secrets -o jsonpath='{.data.MYSQL_ROOT_PASSWORD}' | base64 -d 2>/dev/null || true)"
fi

# 端口转发助手：<port> <service>；已在监听则跳过
start_pf() {
  local port="$1" svc="$2"
  if nc -z 127.0.0.1 "$port" >/dev/null 2>&1; then
    return 0
  fi
  echo ">>> 建立 $svc -> 127.0.0.1:$port 端口转发 ..."
  kubectl -n "$NS" port-forward "svc/$svc" "$port:$port" >/dev/null 2>&1 &
  PF_PIDS+=("$!")
  # 等待端口就绪
  for _ in $(seq 1 30); do
    nc -z 127.0.0.1 "$port" >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "!!! 端口 $port 转发失败（$svc），请检查集群/网络" >&2
  return 1
}

PF_PIDS=()
cleanup() {
  for pid in "${PF_PIDS[@]:-}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

# 先启动转发再校验
start_pf 8123 clickhouse || exit 1
start_pf 3306 mysql || exit 1

export CLICKHOUSE_HOST="${CLICKHOUSE_HOST:-127.0.0.1}"
export CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-8123}"
export CLICKHOUSE_USER="${CLICKHOUSE_USER:-default}"
[ -n "$CH_PASS" ] && export CLICKHOUSE_PASSWORD="$CH_PASS"

export MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
export MYSQL_PORT="${MYSQL_PORT:-3306}"
export MYSQL_USER="${MYSQL_USER:-root}"
[ -n "$MYSQL_PASS" ] && export MYSQL_PASSWORD="$MYSQL_PASS"
export MYSQL_DB="${MYSQL_DB:-aiops}"

echo ">>> 运行 multicluster_demo.py $*（cwd=${ORCH_DIR}）"
cd "$ORCH_DIR"
exec python3 multicluster_demo.py "$@"
