"""知识图谱查询 API（读端点裸奔，仅 POST /build 需 admin）。

挂载约定与 flow_api.py 一致：router 由 main.py include_router 接入。
- 读端点不校验身份（信任 query-api 代理层已完成 JWT 鉴权），直接返回 dict。
- 仅重算类写操作（POST /build）用 X-Internal-Token + X-Internal-Role=admin 双重校验。
- DB 不可达时读端点返回 200 + 空数据 + error 字段，绝不 500。
"""
from __future__ import annotations

import json
import os
from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel

from db import get_conn
import kg_graph

router = APIRouter(prefix="/api/v1/ai/kg")


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


def _require_admin(request: Request):
    """仅 admin 可操作（内部 token + X-Internal-Role=admin），与 main.py 同源。
    kg_api 不能 import main.py（循环导入），故此处内联等价实现。"""
    expected = os.environ.get("INTERNAL_TOKEN", "")
    got = request.headers.get("X-Internal-Token", "")
    if not expected or got != expected:
        raise HTTPException(403, "请求来源不可信（内部 token 校验失败）")
    if request.headers.get("X-Internal-Role", "") != "admin":
        raise HTTPException(403, "仅管理员可操作")


class _BuildBody(BaseModel):
    cluster_id: str = "default"


@router.get("/graph")
def kg_graph_full(cluster_id: str = "default"):
    """全量图（按 props_json 里的 cluster_id 过滤）。"""
    conn = get_conn()
    if conn is None:
        return {"nodes": [], "edges": [], "error": "MySQL 不可用"}
    nodes, edges = [], []
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT id, type, name, props_json FROM topology_nodes")
            for r in cur.fetchall():
                props = _json_loads(r.get("props_json"))
                if str(props.get("cluster_id", "default")) != str(cluster_id):
                    continue
                nodes.append({"id": int(r["id"]), "type": r["type"],
                              "name": r["name"], "props": props})
            cur.execute(
                "SELECT id, src_id, dst_id, type, props_json "
                "FROM topology_relations")
            for r in cur.fetchall():
                props = _json_loads(r.get("props_json"))
                if str(props.get("cluster_id", "default")) != str(cluster_id):
                    continue
                edges.append({"id": int(r["id"]), "src": int(r["src_id"]),
                              "dst": int(r["dst_id"]), "type": r["type"],
                              "props": props})
    except Exception as e:
        return {"nodes": [], "edges": [], "error": f"graph 查询失败: {e}"}
    finally:
        conn.close()
    return {"nodes": nodes, "edges": edges}


@router.get("/entity")
def kg_entity(type: str = "service", name: str = "", cluster_id: str = "default"):
    """按 (type, name, cluster_id) 查单个节点；无则返回 {"entity": null}。"""
    node = kg_graph.get_node(type, name, cluster_id)
    return {"entity": node}


@router.get("/neighbors")
def kg_neighbors(id: int, hops: int = 1, edge_types: str = ""):
    """以节点 id 为中心做无向 BFS，返回 hops 跳内节点与边。"""
    et = None
    if edge_types:
        et = [t.strip() for t in edge_types.split(",") if t.strip()]
    return kg_graph.neighbors(id, hops=hops, edge_types=et)


@router.get("/path")
def kg_path(from_type: str = "service", from_name: str = "",
            to_type: str = "service", to_name: str = "",
            cluster_id: str = "default"):
    """两节点最短路径（节点 id 序列）。"""
    return {"path": kg_graph.shortest_path(
        from_type, from_name, to_type, to_name, cluster_id)}


@router.get("/impact")
def kg_impact(service: str, cluster_id: str = "default", depth: int = 3):
    """服务的下游影响面（沿出边 BFS 闭包）。"""
    node = kg_graph.get_node("service", service, cluster_id)
    if node is None:
        return {"service": service, "nodes": [], "edges": []}
    closure = kg_graph.downstream_closure(int(node["id"]), depth)
    return {"service": service,
            "nodes": closure.get("nodes", []),
            "edges": closure.get("edges", [])}


@router.get("/evidence")
def kg_evidence(entity_id: int, limit: int = 10):
    """节点关联证据：出/入边 + HAS_CHANGE 边对应的 change_events 详情。
    change_events 表不存在时 changes 返回空数组（不 500）。"""
    entity = None
    relations = []
    changes = []
    error = ""
    conn = get_conn()
    if conn is None:
        return {"entity": None, "relations": [], "changes": [],
                "error": "MySQL 不可用"}
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id, type, name, props_json FROM topology_nodes WHERE id=%s",
                (entity_id,))
            row = cur.fetchone()
            if row is not None:
                entity = {"id": int(row["id"]), "type": row["type"],
                          "name": row["name"],
                          "props": _json_loads(row.get("props_json"))}
            cur.execute(
                "SELECT id, src_id, dst_id, type, props_json "
                "FROM topology_relations WHERE src_id=%s OR dst_id=%s "
                "ORDER BY id DESC LIMIT %s",
                (entity_id, entity_id, int(limit)))
            for r in cur.fetchall():
                relations.append({
                    "id": int(r["id"]), "src_id": int(r["src_id"]),
                    "dst_id": int(r["dst_id"]), "type": r["type"],
                    "props": _json_loads(r.get("props_json")),
                })
    except Exception as e:
        entity = None
        relations = []
        error = f"evidence 查询失败: {e}"
    finally:
        conn.close()

    change_ids = []
    for r in relations:
        if r["type"] == "HAS_CHANGE":
            cid = r["props"].get("change_id")
            if cid:
                change_ids.append(str(cid))
    if change_ids:
        c_conn = get_conn()
        if c_conn is None:
            error = error or "MySQL 不可用"
        else:
            try:
                with c_conn.cursor() as cur:
                    ph = ",".join(["%s"] * len(change_ids))
                    cur.execute(
                        f"SELECT id, cluster_id, service, change_type, operator, "
                        f"content, created_at FROM change_events "
                        f"WHERE id IN ({ph}) ORDER BY created_at DESC LIMIT %s",
                        (*change_ids, int(limit)))
                    changes = [dict(row) for row in cur.fetchall()]
            except Exception as e:
                msg = str(e).lower()
                if not ("doesn't exist" in msg or "does not exist" in msg or "1146" in msg):
                    error = error or f"change_events 查询失败: {e}"
                # 表不存在：changes 保持空数组
            finally:
                c_conn.close()

    result = {"entity": entity, "relations": relations, "changes": changes}
    if error:
        result["error"] = error
    return result


@router.post("/build")
def kg_build(body: _BuildBody, request: Request):
    """重建指定集群的知识图谱（仅 admin）。"""
    _require_admin(request)
    return kg_graph.build_all(body.cluster_id)
