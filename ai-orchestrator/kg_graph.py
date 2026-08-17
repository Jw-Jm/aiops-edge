"""AIOps 平台知识图谱构建管线（纯函数模块，可独立测试）。

数据源：
- MySQL `aiops.topology_nodes` / `aiops.topology_relations`（属性图存储，props_json 为 JSON 字符串）。
- ClickHouse `observability.service_topology`（服务调用边，HTTP 访问）。
- query-api `/infrastructure/nodes`、`/infrastructure/pods`（K8s 基础设施，内部 HTTP + X-Internal-Token）。
- MySQL `aiops.change_events`（变更事件，最近 7 天）。

约定：
- 节点/边去重键见各 upsert 函数；`created_by`/`cluster_id` 存放在 props_json 内。
- 本模块不 import main / db.py，独立维护 `_get_conn()`（pymysql）。
- 所有 build_* 函数容错：任何外部依赖不可达时把异常记入返回统计的 errors 列表，绝不抛出。

环境变量：
- MYSQL_HOST/MYSQL_PORT/MYSQL_USER/MYSQL_PASSWORD/MYSQL_DB
- CLICKHOUSE_HOST/CLICKHOUSE_PORT/CLICKHOUSE_USER/CLICKHOUSE_PASSWORD
- QUERY_API_URL / INTERNAL_TOKEN
"""
import json
import os
from collections import deque
from typing import Optional

# ═══════════════════════════════════════════════════════════════
#  常量
# ═══════════════════════════════════════════════════════════════

ENTITY_TYPES = {
    "service", "instance", "middleware", "node", "pod", "cluster",
    "server", "switch", "sensor", "sel_event", "change", "alert", "case",
}

REL_TYPES = {
    "DEPENDS_ON", "RUNS_ON", "CONNECTS_TO", "HAS_CHANGE",
    "RAISES", "CAUSED_BY", "MENTIONED_IN",
}

# 环境配置（模块加载时读取一次，测试可覆盖）
_MYSQL_HOST = os.environ.get("MYSQL_HOST", "127.0.0.1")
_MYSQL_PORT = int(os.environ.get("MYSQL_PORT", "3306"))
_MYSQL_USER = os.environ.get("MYSQL_USER", "root")
_MYSQL_PASSWORD = os.environ.get("MYSQL_PASSWORD", "")
_MYSQL_DB = os.environ.get("MYSQL_DB", "aiops")

_CH_HOST = os.environ.get("CLICKHOUSE_HOST", "clickhouse.observability.svc.cluster.local")
_CH_PORT = os.environ.get("CLICKHOUSE_PORT", "8123")
_CH_USER = os.environ.get("CLICKHOUSE_USER", "default")
_CH_PASSWORD = os.environ.get("CLICKHOUSE_PASSWORD", "")

_QUERY_API_URL = os.environ.get(
    "QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
_INTERNAL_TOKEN = os.environ.get("INTERNAL_TOKEN", "")

# 每次 build 统计（模块级，由 build_* 重置后读取）
_STATS = {
    "nodes_added": 0,
    "nodes_updated": 0,
    "nodes_skipped": 0,
    "edges_added": 0,
    "edges_updated": 0,
    "edges_skipped": 0,
    "errors": [],
}

_JSON_KEYS = ("nodes_added", "nodes_updated", "nodes_skipped",
              "edges_added", "edges_updated", "edges_skipped")


# ═══════════════════════════════════════════════════════════════
#  内部工具
# ═══════════════════════════════════════════════════════════════

def _get_conn():
    """独立的 pymysql 连接（不依赖 db.py，避免耦合）。"""
    import pymysql
    return pymysql.connect(
        host=_MYSQL_HOST, port=_MYSQL_PORT,
        user=_MYSQL_USER, password=_MYSQL_PASSWORD, database=_MYSQL_DB,
        charset="utf8mb4", autocommit=False,
        cursorclass=pymysql.cursors.DictCursor,
    )


def _json_loads(s):
    if not s:
        return {}
    if isinstance(s, dict):
        return dict(s)
    try:
        v = json.loads(s)
        return v if isinstance(v, dict) else {}
    except Exception:
        return {}


def _json_dumps(d):
    try:
        return json.dumps(d, ensure_ascii=False, default=str)
    except Exception:
        return "{}"


def _reset_stats():
    for k in _JSON_KEYS:
        _STATS[k] = 0
    _STATS["errors"] = []


def _snapshot() -> dict:
    return {k: _STATS[k] for k in _JSON_KEYS} | {"errors": list(_STATS["errors"])}


def _ch_query(sql: str, params: Optional[dict] = None) -> list:
    """执行 ClickHouse SELECT（HTTP），返回 dict 行列表；失败抛异常由调用方捕获。"""
    import base64
    import urllib.parse
    import urllib.request
    url = (f"http://{_CH_HOST}:{_CH_PORT}/?query="
           + urllib.parse.quote(sql) + "&default_format=JSONEachRow")
    if params:
        for k, v in params.items():
            url += f"&param_{k}=" + urllib.parse.quote(str(v))
    req = urllib.request.Request(url)
    if _CH_PASSWORD:
        token = base64.b64encode(f"{_CH_USER}:{_CH_PASSWORD}".encode()).decode()
        req.add_header("Authorization", f"Basic {token}")
    with urllib.request.urlopen(req, timeout=20) as resp:
        raw = resp.read().decode("utf-8", errors="replace")
    rows = []
    for line in raw.splitlines():
        if line.strip():
            try:
                rows.append(json.loads(line))
            except Exception:
                pass
    return rows


def _qa_get(path: str) -> dict:
    """GET query-api 内部接口（带 X-Internal-Token）；失败抛异常由调用方捕获。"""
    import urllib.request
    url = _QUERY_API_URL.rstrip("/") + path
    headers = {}
    if _INTERNAL_TOKEN:
        headers["X-Internal-Token"] = _INTERNAL_TOKEN
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read().decode("utf-8", errors="replace"))


def _extract_items(payload) -> list:
    """容错解析列表字段：优先 items/data，其次 nodes/pods 键，兜底直接数组。"""
    if isinstance(payload, dict):
        for k in ("items", "data"):
            v = payload.get(k)
            if isinstance(v, list):
                return v
        for k in ("nodes", "pods"):
            v = payload.get(k)
            if isinstance(v, list):
                return v
        for v in payload.values():
            if isinstance(v, list):
                return v
    elif isinstance(payload, list):
        return payload
    return []


def _node_fields(n) -> tuple:
    name = str(n.get("name", "") or "").strip()
    status = str(n.get("status", "") or "") or "Unknown"
    capacity = {}
    for k in ("cpu", "memory"):
        if n.get(k) is not None:
            capacity[k] = str(n[k])
    if not capacity and isinstance(n.get("capacity"), dict):
        for k, v in n["capacity"].items():
            capacity[k] = str(v)
    return name, status, capacity


def _pod_fields(p) -> tuple:
    metadata = p.get("metadata") if isinstance(p, dict) else None
    name = str(p.get("name", "") or "").strip()
    namespace = str(p.get("namespace", "") or "")
    status = str(p.get("status", "") or "") or "Unknown"
    node_name = str(p.get("node_name") or p.get("node") or "").strip()
    spec = p.get("spec") if isinstance(p, dict) else None
    if not node_name and isinstance(spec, dict):
        node_name = str(spec.get("nodeName", "") or "").strip()
    if isinstance(metadata, dict):
        if not name:
            name = str(metadata.get("name", "") or "").strip()
        if not namespace:
            namespace = str(metadata.get("namespace", "") or "")
    return name, namespace, status, node_name


def _pod_to_service_name(pod_name: Optional[str]) -> list:
    """从 pod 名推导候选服务名（按优先级返回列表，调用方逐个匹配服务节点）。

    - deployment 型：`query-api-7966f8dbb8-sjswt` → 候选 `query-api`（去最后两段 -hash-random）
    - statefulset 型：`mysql-0` / `deepflow-clickhouse-0` → 候选 `mysql` / `deepflow-clickhouse`（去最后一段 -序号）
    - daemonset 型：`deepflow-agent-b88wd` → 候选 `deepflow-agent`（去最后一段 -hash）
    返回去重保序的候选列表；无法推导（无 `-` 分隔）返回空列表。
    """
    if not pod_name:
        return []
    parts = pod_name.split("-")
    cands = []
    if len(parts) >= 3:
        cands.append("-".join(parts[:-2]))  # deployment：去 hash+random
    if len(parts) >= 2:
        cands.append("-".join(parts[:-1]))  # statefulset/daemonset：去序号/hash
    seen = set()
    out = []
    for c in cands:
        if c and c not in seen:
            seen.add(c)
            out.append(c)
    return out


def _find_node_id(node_type: str, node_name: str) -> Optional[int]:
    """查 topology_nodes 返回 id；不存在返回 None。"""
    if not node_name:
        return None
    conn = _get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id FROM topology_nodes WHERE type=%s AND name=%s ORDER BY id LIMIT 1",
                (node_type, node_name))
            row = cur.fetchone()
            return int(row["id"]) if row else None
    except Exception:
        return None
    finally:
        conn.close()


# ═══════════════════════════════════════════════════════════════
#  节点 / 边 upsert
# ═══════════════════════════════════════════════════════════════

def upsert_node(type_, name, props: dict) -> int:
    """按 (type, name, props.cluster_id) 去重 upsert 节点。

    - props 缺 cluster_id 默认 "default"，缺 created_by 默认 "auto"。
    - 已存在且 created_by=="manual"：不改动（返回原 id，记 nodes_skipped）。
    - 已存在且非 manual：合并 props_json（新值覆盖旧值同键），刷新 updated_at。
    - 不存在：INSERT，返回新 id。
    """
    props = dict(props)
    cluster_id = str(props.get("cluster_id", "default"))
    props["cluster_id"] = cluster_id
    props.setdefault("created_by", "auto")
    conn = _get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id, props_json, created_at, updated_at "
                "FROM topology_nodes WHERE type=%s AND name=%s ORDER BY id",
                (type_, name))
            existing = None
            for r in cur.fetchall():
                if str(_json_loads(r.get("props_json")).get("cluster_id", "default")) == cluster_id:
                    existing = r
                    break
            if existing is not None:
                old = _json_loads(existing.get("props_json"))
                if old.get("created_by") == "manual":
                    # 人工节点不可被管线自动修改
                    _STATS["nodes_skipped"] += 1
                    conn.rollback()
                    return int(existing["id"])
                merged = dict(old)
                merged.update(props)
                cur.execute(
                    "UPDATE topology_nodes SET props_json=%s, updated_at=CURRENT_TIMESTAMP "
                    "WHERE id=%s",
                    (_json_dumps(merged), existing["id"]))
                _STATS["nodes_updated"] += 1
                conn.commit()
                return int(existing["id"])
            cur.execute(
                "INSERT INTO topology_nodes (type, name, props_json, created_at, updated_at) "
                "VALUES (%s, %s, %s, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
                (type_, name, _json_dumps(props)))
            _STATS["nodes_added"] += 1
            conn.commit()
            return int(cur.lastrowid)
    except Exception:
        try:
            conn.rollback()
        except Exception:
            pass
        raise
    finally:
        conn.close()


def upsert_edge(src_id, dst_id, rel_type, props: dict) -> int:
    """按 (src_id, dst_id, rel_type) 去重 upsert 边；存在则合并 props。"""
    props = dict(props)
    props.setdefault("cluster_id", "default")
    props.setdefault("created_by", "auto")
    conn = _get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id, props_json FROM topology_relations "
                "WHERE src_id=%s AND dst_id=%s AND type=%s ORDER BY id LIMIT 1",
                (src_id, dst_id, rel_type))
            row = cur.fetchone()
            if row is not None:
                merged = dict(_json_loads(row.get("props_json")))
                merged.update(props)
                cur.execute(
                    "UPDATE topology_relations SET props_json=%s "
                    "WHERE id=%s",
                    (_json_dumps(merged), row["id"]))
                _STATS["edges_updated"] += 1
                conn.commit()
                return int(row["id"])
            cur.execute(
                "INSERT INTO topology_relations (src_id, dst_id, type, props_json, created_at, updated_at) "
                "VALUES (%s, %s, %s, %s, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
                (src_id, dst_id, rel_type, _json_dumps(props)))
            _STATS["edges_added"] += 1
            conn.commit()
            return int(cur.lastrowid)
    except Exception:
        try:
            conn.rollback()
        except Exception:
            pass
        raise
    finally:
        conn.close()


# ═══════════════════════════════════════════════════════════════
#  构建函数
# ═══════════════════════════════════════════════════════════════

def build_from_traces(cluster_id: str = "default") -> dict:
    """从 ClickHouse observability.service_topology 拉最近调用边，建 service 节点与 DEPENDS_ON 边。

    先探测表结构（SELECT * ... LIMIT 1），失败回退 DESCRIBE TABLE，再按发现字段写聚合查询。
    CH 不可达时异常进 errors 列表，不抛出。
    """
    _reset_stats()
    # 1) 探测字段
    cols = []
    try:
        probe = _ch_query("SELECT * FROM observability.service_topology LIMIT 1")
        if probe:
            cols = list(probe[0].keys())
        else:
            desc = _ch_query("DESCRIBE TABLE observability.service_topology")
            cols = [str(d.get("name", "")) for d in desc if d.get("name")]
    except Exception as e:
        _STATS["errors"].append(f"traces probe: {e}")
        return _snapshot()
    colset = set(cols)

    def pick(candidates, default):
        for c in candidates:
            if c in colset:
                return c
        return default

    src_col = pick(["source_service", "source", "src_service"], "source_service")
    tgt_col = pick(["target_service", "target", "dst_service"], "target_service")
    call_col = pick(["call_count", "calls", "count"], "call_count")
    err_col = pick(["error_count", "errors", "errs", "error"], "error_count")

    filters = []
    if "cluster_id" in colset:
        filters.append("cluster_id = {cluster_id:String}")
    if "time_bucket" in colset:
        filters.append("time_bucket >= now() - INTERVAL 1440 MINUTE")
    sql = (f"SELECT {src_col} AS _src, {tgt_col} AS _tgt, "
           f"sum({call_col}) AS _calls, sum({err_col}) AS _errors "
           f"FROM observability.service_topology")
    if filters:
        sql += " WHERE " + " AND ".join(filters)
    sql += " GROUP BY _src, _tgt"

    # 2) 聚合查询
    try:
        rows = _ch_query(sql, params={"cluster_id": cluster_id})
    except Exception as e:
        _STATS["errors"].append(f"traces query: {e}")
        return _snapshot()

    # 3) 落库
    for r in rows:
        src = str(r.get("_src") or "").strip()
        tgt = str(r.get("_tgt") or "").strip()
        if not src or not tgt or src == tgt:
            continue
        try:
            sid = upsert_node("service", src, {"cluster_id": cluster_id, "created_by": "auto"})
            did = upsert_node("service", tgt, {"cluster_id": cluster_id, "created_by": "auto"})
            upsert_edge(sid, did, "DEPENDS_ON", {
                "calls": int(r.get("_calls") or 0),
                "errors": int(r.get("_errors") or 0),
                "cluster_id": cluster_id,
                "created_by": "auto",
            })
        except Exception as e:
            _STATS["errors"].append(f"traces edge {src}->{tgt}: {e}")
    return _snapshot()


def build_from_k8s(cluster_id: str = "default") -> dict:
    """从 query-api 拉 K8s 节点/Pod，建 node/pod 节点与 RUNS_ON 边（pod→node）。

    响应格式容错解析 {items:[...]} / {data:[...]} / {nodes|pods:[...]} / 直接数组。
    异常捕获进 errors 列表，不抛出。
    """
    _reset_stats()
    # 集群节点：从 MySQL clusters 表取真实集群名（kubeconfig != 'mock'），兜底用 cluster_id
    cluster_name = cluster_id
    try:
        conn = _get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute("SELECT name FROM clusters WHERE kubeconfig != 'mock' AND status='active' ORDER BY id LIMIT 1")
                row = cur.fetchone()
                if row and row.get("name"):
                    cluster_name = row["name"]
        finally:
            conn.close()
    except Exception:
        pass
    cid = upsert_node("cluster", cluster_name, {
        "cluster_id": cluster_id, "created_by": "auto",
    })

    try:
        payload = _qa_get("/infrastructure/nodes")
        for n in _extract_items(payload):
            try:
                name, status, capacity = _node_fields(n)
                if not name:
                    continue
                nid = upsert_node("node", name, {
                    "name": name, "status": status, "capacity": capacity,
                    "cluster_id": cluster_id, "created_by": "auto",
                })
                upsert_edge(cid, nid, "CONTAINS", {
                    "cluster_id": cluster_id, "created_by": "auto",
                })
            except Exception as e:
                _STATS["errors"].append(f"k8s node {n.get('name')!r}: {e}")
    except Exception as e:
        _STATS["errors"].append(f"k8s nodes fetch: {e}")

    try:
        payload = _qa_get("/infrastructure/pods")
        for p in _extract_items(payload):
            try:
                name, namespace, status, node_name = _pod_fields(p)
                if not name:
                    continue
                pid = upsert_node("pod", name, {
                    "namespace": namespace, "node_name": node_name, "status": status,
                    "cluster_id": cluster_id, "created_by": "auto",
                })
                if node_name:
                    nid = upsert_node("node", node_name, {
                        "name": node_name, "cluster_id": cluster_id, "created_by": "auto",
                    })
                    upsert_edge(pid, nid, "RUNS_ON", {
                        "cluster_id": cluster_id, "created_by": "auto",
                    })
                # service→pod RUNS_ON：pod 名推导候选服务名（deployment 去两段 / statefulset·daemonset 去一段），
                # 逐个匹配已存在的 service 节点，命中即建边
                for svc_name in _pod_to_service_name(name):
                    sid = _find_node_id("service", svc_name)
                    if sid:
                        upsert_edge(sid, pid, "RUNS_ON", {
                            "cluster_id": cluster_id, "created_by": "auto",
                        })
                        break
            except Exception as e:
                _STATS["errors"].append(f"k8s pod {p.get('name')!r}: {e}")
    except Exception as e:
        _STATS["errors"].append(f"k8s pods fetch: {e}")

    return _snapshot()


def attach_changes(cluster_id: str = "default") -> dict:
    """从 MySQL change_events 读最近 7 天变更，建 change 节点 + HAS_CHANGE 边（service→change）。

    change_events 表不存在时跳过（返回空统计）；查询失败进 errors 列表，不抛出。
    """
    _reset_stats()
    try:
        conn = _get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT id, cluster_id, service, change_type, operator, created_at "
                    "FROM change_events "
                    "WHERE created_at >= NOW() - INTERVAL 7 DAY AND cluster_id = %s "
                    "ORDER BY created_at",
                    (cluster_id,))
                rows = cur.fetchall()
            conn.commit()
        finally:
            conn.close()
    except Exception as e:
        msg = str(e).lower()
        if "doesn't exist" in msg or "does not exist" in msg or "1146" in msg:
            return _snapshot()  # 表未建，跳过
        _STATS["errors"].append(f"changes fetch: {e}")
        return _snapshot()

    for r in rows:
        try:
            cid = int(r["id"])
            change_name = f"change-{cid}"
            chnode_id = upsert_node("change", change_name, {
                "change_id": cid,
                "change_type": str(r.get("change_type") or ""),
                "operator": str(r.get("operator") or ""),
                "service": str(r.get("service") or ""),
                "created_at": str(r.get("created_at") or ""),
                "cluster_id": cluster_id,
                "created_by": "auto",
            })
            svc = str(r.get("service") or "").strip()
            if svc:
                sid = upsert_node("service", svc, {"cluster_id": cluster_id, "created_by": "auto"})
                upsert_edge(sid, chnode_id, "HAS_CHANGE", {
                    "change_id": cid,
                    "change_type": str(r.get("change_type") or ""),
                    "operator": str(r.get("operator") or ""),
                    "created_at": str(r.get("created_at") or ""),
                    "cluster_id": cluster_id,
                })
        except Exception as e:
            _STATS["errors"].append(f"changes row {r.get('id')}: {e}")
    return _snapshot()


def attach_middleware(cluster_id: str = "default") -> dict:
    """从 trace_spans 的 db_system 字段挖掘 service→middleware 依赖边。

    例: orders 调用了 MySQL → middleware 节点 mysql + DEPENDS_ON 边。
    CH 不可达或表缺失时异常进 errors 列表，不抛出。
    """
    _reset_stats()
    sql = ("SELECT service_name, db_system, count() AS c "
           "FROM observability.trace_spans "
           "WHERE db_system != '' AND start_time >= now() - INTERVAL 24 HOUR "
           "GROUP BY service_name, db_system LIMIT 200")
    try:
        rows = _ch_query(sql)
    except Exception as e:
        _STATS["errors"].append(f"middleware query: {e}")
        return _snapshot()
    for r in rows:
        svc = str(r.get("service_name") or "").strip()
        db = str(r.get("db_system") or "").strip()
        c = int(r.get("c") or 0)
        if not svc or not db:
            continue
        try:
            mid = upsert_node("middleware", db, {
                "db_system": db, "cluster_id": cluster_id, "created_by": "auto"})
            sid = get_node("service", svc, cluster_id)
            if not sid:
                sid = upsert_node("service", svc, {
                    "cluster_id": cluster_id, "created_by": "auto"})
            else:
                sid = int(sid["id"])
            upsert_edge(sid, mid, "DEPENDS_ON", {
                "calls": c, "cluster_id": cluster_id, "created_by": "auto"})
        except Exception as e:
            _STATS["errors"].append(f"middleware edge {svc}->{db}: {e}")
    return _snapshot()


def build_all(cluster_id: str = "default") -> dict:
    """依次执行 traces / k8s / middleware / changes 四条管线并汇总统计。"""
    traces = build_from_traces(cluster_id)
    k8s = build_from_k8s(cluster_id)
    middleware = attach_middleware(cluster_id)
    changes = attach_changes(cluster_id)
    total = {}
    for k in _JSON_KEYS:
        total[k] = (traces.get(k, 0) + k8s.get(k, 0)
                    + middleware.get(k, 0) + changes.get(k, 0))
    total["errors"] = (traces.get("errors", [])
                       + k8s.get("errors", [])
                       + middleware.get("errors", [])
                       + changes.get("errors", []))
    return {"traces": traces, "k8s": k8s, "middleware": middleware,
            "changes": changes, "total": total}


# ═══════════════════════════════════════════════════════════════
#  图查询（内存 BFS，节点数 <10 万）
# ═══════════════════════════════════════════════════════════════

def _load_graph():
    """全量加载节点/边（内存图，节点数 <10 万）。返回 (node_rows, rel_rows)。"""
    conn = _get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id, type, name, props_json, created_at, updated_at "
                "FROM topology_nodes")
            node_rows = cur.fetchall()
            cur.execute(
                "SELECT id, src_id, dst_id, type, props_json, created_at "
                "FROM topology_relations")
            rel_rows = cur.fetchall()
        conn.commit()
        return node_rows, rel_rows
    finally:
        conn.close()


def _node_dict(r) -> dict:
    props = _json_loads(r.get("props_json"))
    return {
        "id": int(r["id"]),
        "type": r["type"],
        "name": r["name"],
        "props": props,
        "cluster_id": str(props.get("cluster_id", "default")),
        "created_by": props.get("created_by", ""),
        "created_at": str(r.get("created_at") or ""),
        "updated_at": str(r.get("updated_at") or ""),
    }


def _edge_dict(r) -> dict:
    return {
        "id": int(r["id"]),
        "src_id": int(r["src_id"]),
        "dst_id": int(r["dst_id"]),
        "type": r["type"],
        "props": _json_loads(r.get("props_json")),
        "created_at": str(r.get("created_at") or ""),
    }


def get_node(type_, name, cluster_id: str = "default") -> Optional[dict]:
    """按 (type, name, cluster_id) 查节点；不存在或 DB 不可用时返回 None。"""
    try:
        conn = _get_conn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT id, type, name, props_json, created_at, updated_at "
                    "FROM topology_nodes WHERE type=%s AND name=%s ORDER BY id",
                    (type_, name))
                rows = cur.fetchall()
            conn.commit()
        finally:
            conn.close()
        for r in rows:
            d = _node_dict(r)
            if d["cluster_id"] == str(cluster_id):
                return d
        return None
    except Exception:
        return None


def neighbors(node_id, hops: int = 1, edge_types=None) -> dict:
    """以 node_id 为中心做无向 BFS，返回 hops 跳内节点与边。"""
    node_rows, rel_rows = _load_graph()
    node_map = {int(r["id"]): _node_dict(r) for r in node_rows}
    start = int(node_id)
    if start not in node_map:
        return {"nodes": [], "edges": []}
    edge_types = set(edge_types) if edge_types else None
    adj = {}
    for r in rel_rows:
        e = _edge_dict(r)
        if edge_types and e["type"] not in edge_types:
            continue
        adj.setdefault(e["src_id"], []).append(e)
        adj.setdefault(e["dst_id"], []).append(e)
    visited = {start}
    order = [node_map[start]]
    seen_edges = {}
    queue = deque([(start, 0)])
    while queue:
        cur, depth = queue.popleft()
        if depth >= max(1, int(hops)):
            continue
        for e in adj.get(cur, []):
            eid = e["id"]
            if eid not in seen_edges:
                seen_edges[eid] = e
            other = e["dst_id"] if e["src_id"] == cur else e["src_id"]
            if other not in visited and other in node_map:
                visited.add(other)
                order.append(node_map[other])
                queue.append((other, depth + 1))
    return {"nodes": order, "edges": list(seen_edges.values())}


def downstream_closure(node_id, depth: int = 3) -> dict:
    """沿出边（src→dst）BFS，返回 depth 层下游闭包。"""
    node_rows, rel_rows = _load_graph()
    node_map = {int(r["id"]): _node_dict(r) for r in node_rows}
    start = int(node_id)
    if start not in node_map:
        return {"nodes": [], "edges": []}
    out_adj = {}
    for r in rel_rows:
        e = _edge_dict(r)
        out_adj.setdefault(e["src_id"], []).append(e)
    visited = {start}
    order = [node_map[start]]
    seen_edges = {}
    queue = deque([(start, 0)])
    while queue:
        cur, dep = queue.popleft()
        if dep >= max(1, int(depth)):
            continue
        for e in out_adj.get(cur, []):
            eid = e["id"]
            if eid not in seen_edges:
                seen_edges[eid] = e
            other = e["dst_id"]
            if other not in visited and other in node_map:
                visited.add(other)
                order.append(node_map[other])
                queue.append((other, dep + 1))
    return {"nodes": order, "edges": list(seen_edges.values())}


def shortest_path(src_type, src_name, dst_type, dst_name,
                  cluster_id: str = "default") -> list:
    """求两节点间最短路径（节点 id 序列，无向 BFS；无路径返回空列表）。"""
    s = get_node(src_type, src_name, cluster_id)
    d = get_node(dst_type, dst_name, cluster_id)
    if s is None or d is None:
        return []
    start, goal = int(s["id"]), int(d["id"])
    if start == goal:
        return [start]
    try:
        _, rel_rows = _load_graph()
    except Exception:
        return []
    adj = {}
    for r in rel_rows:
        a, b = int(r["src_id"]), int(r["dst_id"])
        adj.setdefault(a, set()).add(b)
        adj.setdefault(b, set()).add(a)
    prev: dict = {start: None}
    queue = deque([start])
    found = False
    while queue:
        cur = queue.popleft()
        if cur == goal:
            found = True
            break
        for nxt in adj.get(cur, ()):
            if nxt not in prev:
                prev[nxt] = cur
                queue.append(nxt)
    if not found:
        return []
    path = []
    cur = goal
    while cur is not None:
        path.append(cur)
        cur = prev[cur]
    path.reverse()
    return path


# ═══════════════════════════════════════════════════════════════
#  对账
# ═══════════════════════════════════════════════════════════════

def reconcile() -> int:
    """把 created_by=auto、updated_at 早于 7 天且无任何边连接的节点 props 标记 status=stale。

    返回标记数。保留原 updated_at（保持 stale 状态，避免下次又被误判为活跃）。
    """
    marked = 0
    conn = _get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT n.id, n.props_json FROM topology_nodes n "
                "WHERE n.updated_at < NOW() - INTERVAL 7 DAY "
                "AND n.id NOT IN (SELECT src_id FROM topology_relations "
                "                 UNION SELECT dst_id FROM topology_relations)")
            for r in cur.fetchall():
                props = _json_loads(r.get("props_json"))
                if props.get("created_by") != "auto":
                    continue
                if props.get("status") == "stale":
                    continue
                props["status"] = "stale"
                cur.execute(
                    "UPDATE topology_nodes SET props_json=%s, updated_at=updated_at "
                    "WHERE id=%s",
                    (_json_dumps(props), r["id"]))
                marked += 1
            conn.commit()
        return marked
    finally:
        conn.close()
