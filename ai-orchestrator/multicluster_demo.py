"""多集群采集器推广演示（模拟数据实现）。

用途
----
平台数据面已支持多集群：CH 表带 cluster_id、图谱 (cluster_id, name) 复合键。
本脚本模拟 2 个虚拟集群（默认 cluster-b / cluster-c）的采集器数据：

1. 生成模拟 trace（OTLP 语义，服务名带集群特征，调用链 2-3 跳）
   -> 直写 ClickHouse `observability.trace_spans`
2. 聚合模拟服务调用边 -> 直写 ClickHouse `observability.service_topology`
   （kg_graph.build_from_traces 的聚合数据源就是 service_topology）
3. 向 MySQL `aiops.clusters` 注册虚拟集群（按 name 查重，幂等）
4. 触发 `kg_graph.build_all(cluster_id)` 构建图谱（service 节点 + DEPENDS_ON 边）
5. 输出验证摘要：trace 条数 / 服务节点数 / 边数 / 跨集群隔离确认

幂等性
------
- trace_id 固定前缀 `mock-<short>-<序号>`，span_id 由 (cluster,index,hop) 哈希派生；
  每次运行前先删除本脚本写入的 mock 数据（仅限 mock- 前缀与注册的模拟集群），再写入。
- MySQL clusters 按 name 查重，已存在则跳过。
- `--clear` 只删除本脚本创建的模拟数据。

环境变量（与 kg_graph.py 一致）
-------------------------------
- MYSQL_HOST / MYSQL_PORT / MYSQL_USER / MYSQL_PASSWORD / MYSQL_DB
- CLICKHOUSE_HOST / CLICKHOUSE_PORT / CLICKHOUSE_USER / CLICKHOUSE_PASSWORD

CLI
---
    python3 multicluster_demo.py --clusters cluster-b,cluster-c --traces 200
    python3 multicluster_demo.py --clear
"""
import argparse
import base64
import hashlib
import json
import os
import sys
import time
import urllib.parse
import urllib.request
from typing import Optional

# ─────────────────────────────────────────────────────────────
#  常量
# ─────────────────────────────────────────────────────────────

_MYSQL_HOST = os.environ.get("MYSQL_HOST", "127.0.0.1")
_MYSQL_PORT = int(os.environ.get("MYSQL_PORT", "3306"))
_MYSQL_USER = os.environ.get("MYSQL_USER", "root")
_MYSQL_PASSWORD = os.environ.get("MYSQL_PASSWORD", "")
_MYSQL_DB = os.environ.get("MYSQL_DB", "aiops")

_CH_HOST = os.environ.get("CLICKHOUSE_HOST", "127.0.0.1")
_CH_PORT = int(os.environ.get("CLICKHOUSE_PORT", "8123"))
_CH_USER = os.environ.get("CLICKHOUSE_USER", "default")
_CH_PASSWORD = os.environ.get("CLICKHOUSE_PASSWORD", "")

_TENANT = "default"
_MOCK_PREFIX = "mock-"

# 每个模拟集群的服务清单与调用链模板（调用链 2-3 跳）
# chain: 服务名列表，相邻元素构成一条 src->dst 调用边
CLUSTER_TEMPLATES = {
    "cluster-b": {
        "services": ["orders-b", "payments-b", "inventory-b"],
        "chains": [
            ["orders-b", "payments-b"],                 # 2 跳
            ["orders-b", "inventory-b"],                # 2 跳
            ["orders-b", "payments-b", "inventory-b"],  # 3 跳
        ],
    },
    "cluster-c": {
        "services": ["orders-c", "auth-c"],
        "chains": [
            ["orders-c", "auth-c"],                     # 2 跳
        ],
    },
}

# 调用操作语义（src, dst) -> (http_method, operation)
_OPERATIONS = {
    ("orders-b", "payments-b"): ("POST", "/api/payments"),
    ("orders-b", "inventory-b"): ("GET", "/api/inventory"),
    ("payments-b", "inventory-b"): ("GET", "/api/inventory/check"),
    ("orders-c", "auth-c"): ("POST", "/api/auth/login"),
}
_DEFAULT_OP = ("GET", "/api/order")


def _short(cluster_id: str) -> str:
    """cluster-b -> b（用于 trace_id 前缀）。"""
    return cluster_id.rsplit("-", 1)[-1] if "-" in cluster_id else cluster_id


def _hex(s: str, n: int) -> str:
    return hashlib.sha256(s.encode("utf-8")).hexdigest()[:n]


# ─────────────────────────────────────────────────────────────
#  ClickHouse HTTP 访问（直写，参照 kg_graph._ch_query 风格）
# ─────────────────────────────────────────────────────────────

def _ch_request(sql: str, method: str = "POST", data=None,
                default_format: Optional[str] = None) -> str:
    """执行 ClickHouse HTTP 请求；失败抛异常由调用方容错。"""
    url = (f"http://{_CH_HOST}:{_CH_PORT}/?query=" + urllib.parse.quote(sql))
    if default_format:
        url += f"&default_format={urllib.parse.quote(default_format)}"
    headers = {}
    if _CH_PASSWORD:
        token = base64.b64encode(f"{_CH_USER}:{_CH_PASSWORD}".encode()).decode()
        headers["Authorization"] = f"Basic {token}"
    if data is None:
        req = urllib.request.Request(url, headers=headers, method=method)
    else:
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read().decode("utf-8", errors="replace")


def _ch_query(sql: str) -> list:
    """SELECT 查询，返回 dict 行列表（JSONEachRow）。"""
    raw = _ch_request(sql, method="GET", default_format="JSONEachRow")
    rows = []
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except Exception:
            pass
    return rows


def _ch_insert(table: str, rows: list) -> int:
    """以 JSONEachRow 批量写入；返回写入行数。"""
    if not rows:
        return 0
    body = "".join(json.dumps(r, ensure_ascii=False, default=str) + "\n" for r in rows)
    sql = f"INSERT INTO {table} FORMAT JSONEachRow"
    _ch_request(sql, method="POST", data=body.encode("utf-8"))
    return len(rows)


def _ch_delete(table: str, where: str) -> int:
    """轻量 DELETE；不支持时回退 ALTER ... DELETE + OPTIMIZE。"""
    try:
        _ch_request(f"DELETE FROM {table} WHERE {where}", method="POST")
        return -1  # 实际删除数由查询确认
    except Exception:
        try:
            _ch_request(f"ALTER TABLE {table} DELETE WHERE {where}", method="POST")
            _ch_request(f"OPTIMIZE TABLE {table} FINAL", method="POST")
        except Exception:
            pass
        return -1


def ch_available() -> bool:
    try:
        _ch_request("SELECT 1", method="GET")
        return True
    except Exception:
        return False


# ─────────────────────────────────────────────────────────────
#  MySQL 访问（pymysql，参照 kg_graph._get_conn 风格）
# ─────────────────────────────────────────────────────────────

def _mysql_conn():
    import pymysql
    return pymysql.connect(
        host=_MYSQL_HOST, port=_MYSQL_PORT,
        user=_MYSQL_USER, password=_MYSQL_PASSWORD, database=_MYSQL_DB,
        charset="utf8mb4", autocommit=True,
        cursorclass=pymysql.cursors.DictCursor,
    )


def mysql_available() -> bool:
    try:
        conn = _mysql_conn()
        with conn.cursor() as cur:
            cur.execute("SELECT 1")
        conn.close()
        return True
    except Exception:
        return False


# ─────────────────────────────────────────────────────────────
#  模拟数据生成（OTLP 语义）
# ─────────────────────────────────────────────────────────────

def _span_row(cluster_id, trace_id, span_id, parent_span_id, service_name,
              operation, http_method, start_time_str, duration_ns, is_error,
              base_date, base_minute):
    status_code = 500 if is_error else 0
    http_status = 500 if is_error else 200
    return {
        "tenant_id": _TENANT,
        "cluster_id": cluster_id,
        "trace_id": trace_id,
        "span_id": span_id,
        "parent_span_id": parent_span_id,
        "service_name": service_name,
        "operation_name": f"HTTP {http_method} {operation}",
        "span_kind": "SERVER",
        "status_code": status_code,
        "start_time": start_time_str,
        "duration_ns": duration_ns,
        "attributes": {"http.url": operation, "peer": service_name},
        "http_method": http_method,
        "http_status_code": http_status,
        "http_url": operation,
        "db_system": "",
        "db_statement": "",
        "rpc_system": "",
        "service_instance_id": service_name,
        "k8s_namespace": f"ns-{_short(cluster_id)}",
        "k8s_pod_name": f"{service_name}-pod",
        "is_slow": 1 if duration_ns >= 500_000_000 else 0,
        "is_error": 1 if is_error else 0,
        "time_bucket": base_minute,
        "date": base_date,
    }


def generate_spans(cluster_id: str, n_traces: int) -> list:
    """生成 n_traces 条模拟 trace 的 span 行（2-3 跳调用链）。"""
    spec = CLUSTER_TEMPLATES[cluster_id]
    chains = spec["chains"]
    short = _short(cluster_id)
    base = time.time() - 60  # 最近 1 分钟内，保证 build 窗口可见
    base_date = time.strftime("%Y-%m-%d", time.localtime(base))
    base_minute = time.strftime("%Y-%m-%d %H:%M:%S",
                                time.localtime(base - base % 60))
    rows = []
    for i in range(n_traces):
        chain = chains[i % len(chains)]
        trace_id = f"{_MOCK_PREFIX}{short}-{i:05d}"
        prev_span = ""
        # 每条 trace 内按 2ms 递增，保证瀑布图有序
        for hop, svc in enumerate(chain):
            span_id = _hex(f"{cluster_id}:{i}:{hop}:{svc}", 16)
            # SERVER span 语义：root 服务承载入口操作，下游服务承载其入边调用操作
            if hop == 0:
                method, operation = _DEFAULT_OP
            else:
                method, operation = _OPERATIONS.get(
                    (chain[hop - 1], svc), _DEFAULT_OP)
            # 可预测的 start_time：基分 + i 秒 + hop*2ms
            ts = base + i * 5 + hop * 0.002
            start_str = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(ts)) + \
                        f".{int((ts % 1) * 1_000_000_000):09d}"
            dur = 2_000_000 + (i % 7) * 1_000_000 + hop * 500_000
            is_err = 1 if i % 50 == 0 and hop == len(chain) - 1 else 0
            rows.append(_span_row(
                cluster_id, trace_id, span_id, prev_span, svc, operation, method,
                start_str, dur, is_err, base_date, base_minute))
            prev_span = span_id
    return rows


def generate_edges(cluster_id: str, n_traces: int) -> list:
    """按调用链模板聚合 (src, dst) 边统计 -> service_topology 行。"""
    spec = CLUSTER_TEMPLATES[cluster_id]
    chains = spec["chains"]
    agg = {}
    base = time.time() - 60
    base_date = time.strftime("%Y-%m-%d", time.localtime(base))
    base_minute = time.strftime("%Y-%m-%d %H:%M:%S",
                                time.localtime(base - base % 60))
    for i in range(n_traces):
        chain = chains[i % len(chains)]
        for hop in range(len(chain) - 1):
            src, dst = chain[hop], chain[hop + 1]
            key = (src, dst)
            a = agg.setdefault(key, {"calls": 0, "errors": 0, "dur": 0})
            a["calls"] += 1
            a["dur"] += 2_000_000 + (i % 7) * 1_000_000
            if i % 50 == 0:
                a["errors"] += 1
    rows = []
    for (src, dst), a in sorted(agg.items()):
        avg_dur = int(a["dur"] / a["calls"]) if a["calls"] else 0
        rows.append({
            "tenant_id": _TENANT,
            "cluster_id": cluster_id,
            "source_service": src,
            "target_service": dst,
            "time_bucket": base_minute,
            "call_count": a["calls"],
            "error_count": a["errors"],
            "avg_duration_ns": avg_dur,
            "date": base_date,
        })
    return rows


# ─────────────────────────────────────────────────────────────
#  MySQL clusters 注册
# ─────────────────────────────────────────────────────────────

def register_clusters(cluster_ids: list) -> dict:
    """向 aiops.clusters 注册模拟集群（按 name 查重，幂等）。返回 {cluster: action}。"""
    result = {}
    try:
        conn = _mysql_conn()
        try:
            with conn.cursor() as cur:
                for cid in cluster_ids:
                    cur.execute("SELECT id FROM clusters WHERE name=%s", (cid,))
                    if cur.fetchone() is not None:
                        result[cid] = "exists"
                        continue
                    cur.execute(
                        "INSERT INTO clusters (name, provider, region, version, "
                        "node_count, status, api_server, kubeconfig) "
                        "VALUES (%s, 'mock', '', 'mock', 0, 'active', '', 'mock')",
                        (cid,))
                    result[cid] = "registered"
        finally:
            conn.close()
    except Exception as e:
        for cid in cluster_ids:
            result[cid] = f"error: {e}"
    return result


# ─────────────────────────────────────────────────────────────
#  数据写入（幂等：先删 mock 前缀，再写入）
# ─────────────────────────────────────────────────────────────

def write_mock_data(cluster_ids: list, n_traces: int) -> dict:
    """写入模拟 trace_spans + service_topology。返回各集群统计。"""
    summary = {}
    for cid in cluster_ids:
        if cid not in CLUSTER_TEMPLATES:
            summary[cid] = {"error": f"no template for cluster {cid!r}"}
            continue
        short = _short(cid)
        # 1) 清理本脚本历史 mock 数据（仅 mock- 前缀 + 该集群）
        _ch_delete("observability.trace_spans",
                   f"cluster_id = '{cid}' AND trace_id LIKE 'mock-{short}-%'")
        _ch_delete("observability.service_topology",
                   f"cluster_id = '{cid}'")
        # 2) 写入
        spans = generate_spans(cid, n_traces)
        edges = generate_edges(cid, n_traces)
        n_spans = _ch_insert("observability.trace_spans", spans)
        n_edges = _ch_insert("observability.service_topology", edges)
        summary[cid] = {"traces": n_traces, "spans": n_spans, "edges": n_edges}
    return summary


def clear_mock_data(cluster_ids: list) -> dict:
    """删除本脚本创建的全部模拟数据（trace_spans / service_topology / 图谱节点/边 / clusters）。"""
    cleared = {}
    # ClickHouse
    _ch_delete("observability.trace_spans", f"trace_id LIKE '{_MOCK_PREFIX}%'")
    _ch_delete("observability.service_topology",
               "cluster_id IN (" + ",".join(f"'{c}'" for c in cluster_ids) + ")")
    cleared["clickhouse"] = "deleted mock-* traces + mock cluster topology edges"
    if not mysql_available():
        cleared["mysql"] = "skipped (MySQL unreachable)"
        return cleared
    try:
        conn = _mysql_conn()
        try:
            with conn.cursor() as cur:
                # 图谱节点/边（props_json 内含 cluster_id 的模拟集群）
                cond = " OR ".join(
                    f"props_json LIKE '%\"cluster_id\": \"{c}\"%'" for c in cluster_ids)
                cur.execute(
                    f"SELECT id FROM topology_nodes WHERE ({cond})")
                ids = [r["id"] for r in cur.fetchall()]
                for node_id in ids:
                    cur.execute(
                        "DELETE FROM topology_relations WHERE src_id=%s OR dst_id=%s",
                        (node_id, node_id))
                if ids:
                    fmt = ",".join(["%s"] * len(ids))
                    cur.execute(f"DELETE FROM topology_nodes WHERE id IN ({fmt})", ids)
                # clusters（仅删除本脚本创建的 kubeconfig='mock' 行）
                for cid in cluster_ids:
                    cur.execute(
                        "DELETE FROM clusters WHERE name=%s AND kubeconfig='mock'",
                        (cid,))
                cleared["mysql"] = f"deleted {len(ids)} topology nodes + mock clusters rows"
        finally:
            conn.close()
    except Exception as e:
        cleared["mysql"] = f"error: {e}"
    return cleared


# ─────────────────────────────────────────────────────────────
#  图谱构建 + 验证摘要
# ─────────────────────────────────────────────────────────────

def build_graph(cluster_id: str) -> dict:
    """调用 kg_graph.build_all 构建图谱（服务节点 + DEPENDS_ON 边）。"""
    try:
        from kg_graph import build_all
        return build_all(cluster_id)
    except Exception as e:
        return {"total": {"errors": [f"kg_graph build_all failed: {e}"]}}


def verify_cluster(cluster_id: str) -> dict:
    """输出单个集群的验证摘要。"""
    short = _short(cluster_id)
    # trace 条数（按 trace_id 去重）
    rows = _ch_query(
        f"SELECT count(DISTINCT trace_id) AS c FROM observability.trace_spans "
        f"WHERE cluster_id = '{cluster_id}' AND trace_id LIKE 'mock-{short}-%'")
    n_traces = int(rows[0]["c"]) if rows else 0
    # 服务节点数 / DEPENDS_ON 边数（props_json 含 cluster_id）
    n_nodes = n_edges = -1
    if mysql_available():
        conn = _mysql_conn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT count(*) AS c FROM topology_nodes "
                    "WHERE type='service' AND props_json LIKE %s",
                    (f'%"{cluster_id}"%',))
                row = cur.fetchone()
                n_nodes = int(row["c"]) if row else 0
                cur.execute(
                    "SELECT count(*) AS c FROM topology_relations "
                    "WHERE type='DEPENDS_ON' AND props_json LIKE %s",
                    (f'%"{cluster_id}"%',))
                row = cur.fetchone()
                n_edges = int(row["c"]) if row else 0
        finally:
            conn.close()
    return {
        "cluster": cluster_id,
        "traces": n_traces,
        "service_nodes": n_nodes,
        "depends_on_edges": n_edges,
    }


def verify_isolation(cluster_ids: list) -> str:
    """跨集群隔离：orders-b 与 orders-c 必须是不同图节点 id。"""
    try:
        from kg_graph import get_node
        nodes = {}
        for cid in cluster_ids:
            short = _short(cid)
            node = get_node("service", f"orders-{short}", cid)
            nodes[cid] = None if node is None else int(node["id"])
        if all(v is not None for v in nodes.values()):
            ids = list(nodes.values())
            same = len(set(ids)) == 1
            detail = " / ".join(f"{c}=id{nodes[c]}" for c in cluster_ids)
            if same:
                return (f"WARNING: 跨集群服务解析到同一图节点 id（{detail}），"
                        "图谱隔离可能失效")
            return f"OK: 跨集群隔离正常，{detail}（不同节点 id）"
        return "WARNING: 部分模拟服务未在图谱中找到节点，无法完成隔离校验"
    except Exception as e:
        return f"ERROR: 隔离校验失败: {e}"


# ─────────────────────────────────────────────────────────────
#  主流程
# ─────────────────────────────────────────────────────────────

def run_demo(cluster_ids: list, n_traces: int) -> dict:
    report = {"clusters": cluster_ids, "traces_per_cluster": n_traces}
    print("=" * 70)
    print("多集群采集器推广演示（模拟数据）")
    print("=" * 70)
    print(f"模拟集群: {', '.join(cluster_ids)}   每集群 trace 数: {n_traces}")
    print(f"CH  {_CH_HOST}:{_CH_PORT}    MySQL {_MYSQL_HOST}:{_MYSQL_PORT}")

    if not ch_available():
        print("\n[FATAL] ClickHouse 不可达，跳过数据写入。")
        report["clickhouse"] = "unreachable"
        return report

    # 1) 注册集群
    print("\n[1/4] 注册模拟集群到 MySQL clusters ...")
    reg = register_clusters(cluster_ids)
    for cid, action in reg.items():
        print(f"  - {cid}: {action}")
    report["clusters_registered"] = reg

    # 2) 写入模拟数据
    print("\n[2/4] 写入模拟 trace / service_topology ...")
    write = write_mock_data(cluster_ids, n_traces)
    for cid, st in write.items():
        if "error" in st:
            print(f"  - {cid}: {st['error']}")
        else:
            print(f"  - {cid}: {st['traces']} traces / {st['spans']} spans "
                  f"/ {st['edges']} topology edges")
    report["write"] = write

    # 3) 触发图谱构建
    print("\n[3/4] 触发 kg_graph.build_all ...")
    builds = {}
    for cid in cluster_ids:
        if cid not in CLUSTER_TEMPLATES:
            continue
        res = build_graph(cid)
        total = res.get("total", {})
        errs = total.get("errors", [])
        print(f"  - {cid}: nodes(+{total.get('nodes_added', 0)}/"
              f"upd {total.get('nodes_updated', 0)}) "
              f"edges(+{total.get('edges_added', 0)}/upd {total.get('edges_updated', 0)})"
              + (f"  errors={len(errs)}" if errs else ""))
        if errs:
            for e in errs[:3]:
                print(f"      * {e}")
        builds[cid] = res
    report["build"] = builds

    # 4) 验证摘要
    print("\n[4/4] 验证摘要 ...")
    ver = {cid: verify_cluster(cid) for cid in cluster_ids if cid in CLUSTER_TEMPLATES}
    header = f"{'cluster':<12}{'traces':>8}{'svc_nodes':>12}{'depends_on':>12}"
    print("  " + header)
    for cid, v in ver.items():
        print(f"  {v['cluster']:<12}{v['traces']:>8}{v['service_nodes']:>12}"
              f"{v['depends_on_edges']:>12}")
    iso = verify_isolation(cluster_ids)
    print(f"  跨集群隔离: {iso}")
    report["verify"] = ver
    report["isolation"] = iso
    return report


def main():
    ap = argparse.ArgumentParser(
        description="多集群采集器推广演示（模拟数据）")
    ap.add_argument("--clusters", default="cluster-b,cluster-c",
                    help="模拟集群名列表，逗号分隔（默认 cluster-b,cluster-c）")
    ap.add_argument("--traces", type=int, default=200,
                    help="每集群生成的模拟 trace 条数（默认 200）")
    ap.add_argument("--clear", action="store_true",
                    help="删除本脚本创建的全部模拟数据")
    args = ap.parse_args()

    cluster_ids = [c.strip() for c in args.clusters.split(",") if c.strip()]
    if not cluster_ids:
        print("错误: --clusters 不能为空")
        sys.exit(2)

    if args.clear:
        if not ch_available() and not mysql_available():
            print("[FATAL] ClickHouse 与 MySQL 均不可达，无法清理。")
            sys.exit(1)
        print("清理模拟数据 ...")
        res = clear_mock_data(cluster_ids)
        for k, v in res.items():
            print(f"  - {k}: {v}")
        return

    run_demo(cluster_ids, args.traces)


if __name__ == "__main__":
    main()
