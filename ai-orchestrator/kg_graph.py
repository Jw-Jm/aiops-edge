"""AIOps 平台知识图谱构建管线（纯函数模块，可独立测试）。

数据源与持久化均经 query-api 内部边界访问：
- 事实查询走 canonical `/internal/v1/query/*`。
- 属性图读写走 `/internal/v1/control-plane/knowledge-graph`。

约定：
- 节点/边去重键见各 upsert 函数；`created_by`/`cluster_id` 存放在 props_json 内。
- 本模块不 import `db.py`、`pymysql` 或 ClickHouse client。
- 所有 build_* 函数容错：任何外部依赖不可达时把异常记入返回统计的 errors 列表，绝不抛出。

环境变量：QUERY_API_URL、INTERNAL_TOKEN、TRUSTED_CONTEXT_PRIVATE_KEY、AIOPS_SYSTEM_TENANT_ID。
"""
import json
import os
from collections import deque
from datetime import datetime, timedelta, timezone
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

_SYSTEM_TENANT_ID = os.environ.get(
    "AIOPS_SYSTEM_TENANT_ID", "7ed01afc-cc79-4ecd-8767-a2befa6168ad")
_SYSTEM_CLUSTER_ID = os.environ.get("AIOPS_SYSTEM_CLUSTER_ID", "default")
_QUERY_TOOLS = {
    "topology": "query_topology.v1",
    "kubernetes": "query_k8s.v1",
    "changes": "query_changes.v1",
}

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

def _control_plane_factory():
    from control_plane_client import ControlPlaneClient
    return ControlPlaneClient()


def _kg_request(operation: str, body: dict, *, write: bool = False) -> dict:
    return _control_plane_factory().knowledge_graph(operation, body, write=write)


def _query_source(operation: str, cluster_id: str, params: Optional[dict] = None) -> dict:
    """Read source facts through the canonical query-api boundary."""
    from internal_query_client import InternalQueryClient
    from trusted_context_issuer import TrustedContextIssuer
    from internal_query import _load_private_key
    from uuid import uuid4

    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    client = InternalQueryClient(issuer=TrustedContextIssuer(private_key=private_key))
    result = client.query(
        tool_id=_QUERY_TOOLS[operation], operation=operation,
        tenant_id=_SYSTEM_TENANT_ID, cluster_id=str(cluster_id),
        params=params or {}, context_ref=f"knowledge-graph:{operation}:{uuid4()}")
    body = result.body
    data = body.get("data") if isinstance(body, dict) else None
    return data if isinstance(data, dict) else body


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
    """通过 query-api 查图节点 id；不存在返回 None。"""
    if not node_name:
        return None
    try:
        entity = _kg_request("find_node", {
            "type": node_type, "name": node_name, "cluster_id": "",
        }).get("entity")
        return int(entity["id"]) if entity else None
    except Exception:
        return None


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
    result = _kg_request("upsert_node", {
        "type": type_, "name": name, "cluster_id": cluster_id, "props": props,
    }, write=True)
    if result.get("skipped"):
        _STATS["nodes_skipped"] += 1
    elif result.get("created"):
        _STATS["nodes_added"] += 1
    else:
        _STATS["nodes_updated"] += 1
    return int(result["id"])


def upsert_edge(src_id, dst_id, rel_type, props: dict) -> int:
    """按 (src_id, dst_id, rel_type) 去重 upsert 边；存在则合并 props。"""
    props = dict(props)
    props.setdefault("cluster_id", "default")
    props.setdefault("created_by", "auto")
    result = _kg_request("upsert_edge", {
        "src_id": int(src_id), "dst_id": int(dst_id),
        "cluster_id": str(props.get("cluster_id", "default")),
        "edge_type": rel_type, "edge_props": props,
    }, write=True)
    if result.get("created"):
        _STATS["edges_added"] += 1
    else:
        _STATS["edges_updated"] += 1
    return int(result["id"])


# ═══════════════════════════════════════════════════════════════
#  构建函数
# ═══════════════════════════════════════════════════════════════

def build_from_traces(cluster_id: str = _SYSTEM_CLUSTER_ID) -> dict:
    """从 query-api canonical topology 事实建 service 节点与依赖边。"""
    _reset_stats()
    try:
        payload = _query_source("topology", cluster_id, {"minutes": 1440})
    except Exception as e:
        _STATS["errors"].append(f"traces query: {e}")
        return _snapshot()

    for r in payload.get("edges", []) if isinstance(payload, dict) else []:
        src = str(r.get("source") or r.get("source_service") or "").strip()
        tgt = str(r.get("target") or r.get("target_service") or "").strip()
        if not src or not tgt or src == tgt:
            continue
        try:
            sid = upsert_node("service", src, {"cluster_id": cluster_id, "created_by": "auto"})
            did = upsert_node("service", tgt, {"cluster_id": cluster_id, "created_by": "auto"})
            upsert_edge(sid, did, "DEPENDS_ON", {
                "calls": int(r.get("calls") or 0),
                "errors": int(r.get("errors") or 0),
                "cluster_id": cluster_id,
                "created_by": "auto",
            })
        except Exception as e:
            _STATS["errors"].append(f"traces edge {src}->{tgt}: {e}")
    return _snapshot()


def build_from_k8s(cluster_id: str = _SYSTEM_CLUSTER_ID) -> dict:
    """从 query-api 拉 K8s 节点/Pod，建 node/pod 节点与 RUNS_ON 边（pod→node）。

    响应格式容错解析 {items:[...]} / {data:[...]} / {nodes|pods:[...]} / 直接数组。
    异常捕获进 errors 列表，不抛出。
    """
    _reset_stats()
    cluster_name = cluster_id
    payload = {}
    try:
        payload = _query_source("kubernetes", cluster_id, {"namespace": "all"})
    except Exception as e:
        _STATS["errors"].append(f"k8s query: {e}")
    cid = upsert_node("cluster", cluster_name, {
        "cluster_id": cluster_id, "created_by": "auto",
    })

    try:
        for n in (payload.get("node_details") or []) if isinstance(payload, dict) else []:
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
        for p in (payload.get("pods") or []) if isinstance(payload, dict) else []:
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


def attach_changes(cluster_id: str = _SYSTEM_CLUSTER_ID) -> dict:
    """从 canonical changes 查询读取最近 7 天变更，建 change 节点与 HAS_CHANGE 边。"""
    _reset_stats()
    try:
        since = (datetime.now(timezone.utc) - timedelta(days=7)).isoformat()
        payload = _query_source("changes", cluster_id, {"since": since, "limit": 200})
        rows = payload.get("changes", []) if isinstance(payload, dict) else []
    except Exception as e:
        _STATS["errors"].append(f"changes fetch: {e}")
        return _snapshot()

    for r in rows:
        try:
            cid = str(r.get("change_id") or r.get("id") or "")
            if not cid:
                continue
            change_name = f"change-{cid}"
            chnode_id = upsert_node("change", change_name, {
                "change_id": cid,
                "change_type": str(r.get("change_type") or ""),
                "operator": str(r.get("actor") or r.get("operator") or ""),
                "service": str(r.get("service") or r.get("service_name") or ""),
                "created_at": str(r.get("start_time") or r.get("created_at") or ""),
                "summary": str(r.get("summary") or r.get("content") or ""),
                "revision": str(r.get("revision") or ""),
                "cluster_id": cluster_id,
                "created_by": "auto",
            })
            svc = str(r.get("service") or r.get("service_name") or "").strip()
            if svc:
                sid = upsert_node("service", svc, {"cluster_id": cluster_id, "created_by": "auto"})
                upsert_edge(sid, chnode_id, "HAS_CHANGE", {
                    "change_id": cid,
                    "change_type": str(r.get("change_type") or ""),
                    "operator": str(r.get("actor") or r.get("operator") or ""),
                    "created_at": str(r.get("start_time") or r.get("created_at") or ""),
                    "cluster_id": cluster_id,
                })
        except Exception as e:
            _STATS["errors"].append(f"changes row {r.get('id')}: {e}")
    return _snapshot()


def attach_middleware(cluster_id: str = _SYSTEM_CLUSTER_ID) -> dict:
    """从 trace_spans 的 db_system 字段挖掘 service→middleware 依赖边。

    例: orders 调用了 MySQL → middleware 节点 mysql + DEPENDS_ON 边。
    CH 不可达或表缺失时异常进 errors 列表，不抛出。
    """
    _reset_stats()
    try:
        payload = _query_source("middleware", cluster_id, {"minutes": 1440})
        rows = payload.get("middleware", []) if isinstance(payload, dict) else []
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


def build_all(cluster_id: str = _SYSTEM_CLUSTER_ID) -> dict:
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
    """从 query-api 加载全量图快照（节点数 <10 万）。"""
    payload = _kg_request("snapshot", {"cluster_id": ""})
    return payload.get("nodes", []), payload.get("edges", [])


def _node_dict(r) -> dict:
    props = _json_loads(r.get("props", r.get("props_json")))
    return {
        "id": int(r["id"]),
        "type": r["type"],
        "name": r["name"],
        "props": props,
        "cluster_id": str(r.get("cluster_id", props.get("cluster_id", "default"))),
        "created_by": props.get("created_by", ""),
        "created_at": str(r.get("created_at") or ""),
        "updated_at": str(r.get("updated_at") or ""),
    }


def _edge_dict(r) -> dict:
    props = _json_loads(r.get("props", r.get("props_json")))
    return {
        "id": int(r["id"]),
        "src_id": int(r.get("src_id", r.get("src"))),
        "dst_id": int(r.get("dst_id", r.get("dst"))),
        "type": r["type"],
        "props": props,
        "created_at": str(r.get("created_at") or ""),
    }


def get_node(type_, name, cluster_id: str = "default") -> Optional[dict]:
    """按 (type, name, cluster_id) 经 query-api 查节点。"""
    try:
        entity = _kg_request("find_node", {
            "type": type_, "name": name, "cluster_id": str(cluster_id),
        }).get("entity")
        return _node_dict(entity) if entity else None
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
    try:
        return int(_kg_request("reconcile", {}, write=True).get("marked", 0))
    except Exception:
        return 0
