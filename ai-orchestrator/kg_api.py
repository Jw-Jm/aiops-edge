"""知识图谱查询 API（读端点裸奔，仅 POST /build 需 admin）。

挂载约定与 flow_api.py 一致：router 由 main.py include_router 接入。
- 读端点不校验身份（信任 query-api 代理层已完成 JWT 鉴权），直接返回 dict。
- 仅重算类写操作（POST /build）用 X-Internal-Token + X-Internal-Role=admin 双重校验。
- 数据边界不可达时读端点返回 200 + 空数据 + error 字段，绝不 500。
"""
from __future__ import annotations

import os
from fastapi import APIRouter, HTTPException, Request
from pydantic import BaseModel

import kg_graph

router = APIRouter(prefix="/api/v1/ai/kg")


def _require_legacy_backend():
    """The old integer-ID facade is disabled once native graph backends run."""
    if os.environ.get("GRAPH_BACKEND", "legacy_mysql").strip().lower() != "legacy_mysql":
        raise HTTPException(503, "GRAPH_FEATURE_UNAVAILABLE_LEGACY")


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
    _require_legacy_backend()
    try:
        node_rows, rel_rows = kg_graph._load_graph()
        nodes = []
        for row in node_rows:
            node = kg_graph._node_dict(row)
            if node["cluster_id"] == str(cluster_id):
                nodes.append(node)
        allowed = {n["id"] for n in nodes}
        edges = []
        for row in rel_rows:
            edge = kg_graph._edge_dict(row)
            if edge["src_id"] not in allowed or edge["dst_id"] not in allowed:
                continue
            edges.append({"id": edge["id"], "src": edge["src_id"],
                          "dst": edge["dst_id"], "type": edge["type"],
                          "props": edge["props"]})
    except Exception as e:
        return {"nodes": [], "edges": [], "error": f"graph 查询失败: {e}"}
    return {"nodes": nodes, "edges": edges}


@router.get("/entity")
def kg_entity(type: str = "service", name: str = "", cluster_id: str = "default"):
    """按 (type, name, cluster_id) 查单个节点；无则返回 {"entity": null}。"""
    _require_legacy_backend()
    node = kg_graph.get_node(type, name, cluster_id)
    return {"entity": node}


@router.get("/neighbors")
def kg_neighbors(id: int, hops: int = 1, edge_types: str = ""):
    """以节点 id 为中心做无向 BFS，返回 hops 跳内节点与边。"""
    _require_legacy_backend()
    et = None
    if edge_types:
        et = [t.strip() for t in edge_types.split(",") if t.strip()]
    return kg_graph.neighbors(id, hops=hops, edge_types=et)


@router.get("/path")
def kg_path(from_type: str = "service", from_name: str = "",
            to_type: str = "service", to_name: str = "",
            cluster_id: str = "default"):
    """两节点最短路径（节点 id 序列）。"""
    _require_legacy_backend()
    return {"path": kg_graph.shortest_path(
        from_type, from_name, to_type, to_name, cluster_id)}


@router.get("/impact")
def kg_impact(service: str, cluster_id: str = "default", depth: int = 3):
    """服务的下游影响面（沿出边 BFS 闭包）。"""
    _require_legacy_backend()
    node = kg_graph.get_node("service", service, cluster_id)
    if node is None:
        return {"service": service, "nodes": [], "edges": []}
    closure = kg_graph.downstream_closure(int(node["id"]), depth)
    return {"service": service,
            "nodes": closure.get("nodes", []),
            "edges": closure.get("edges", [])}


@router.get("/evidence")
def kg_evidence(entity_id: int, limit: int = 10):
    """节点关联证据：出/入边 + 图中 change 节点的结构化详情。"""
    _require_legacy_backend()
    try:
        node_rows, rel_rows = kg_graph._load_graph()
        nodes = {int(row["id"]): kg_graph._node_dict(row) for row in node_rows}
        entity = nodes.get(int(entity_id))
        relations = []
        for row in rel_rows:
            edge = kg_graph._edge_dict(row)
            if entity_id not in (edge["src_id"], edge["dst_id"]):
                continue
            relations.append(edge)
        relations.sort(key=lambda item: item["id"], reverse=True)
        relations = relations[:max(0, int(limit))]
    except Exception as e:
        return {"entity": None, "relations": [], "changes": [],
                "error": f"evidence 查询失败: {e}"}

    changes = []
    for relation in relations:
        if relation["type"] != "HAS_CHANGE":
            continue
        change = nodes.get(relation["dst_id"])
        if change and change.get("type") == "change":
            props = change.get("props", {})
            changes.append({
                "id": props.get("change_id", change["name"]),
                "cluster_id": props.get("cluster_id", "default"),
                "service": props.get("service", ""),
                "change_type": props.get("change_type", ""),
                "operator": props.get("operator", ""),
                "created_at": props.get("created_at", ""),
            })

    return {"entity": entity, "relations": relations, "changes": changes}


@router.post("/build")
def kg_build(body: _BuildBody, request: Request):
    """重建指定集群的知识图谱（仅 admin）。"""
    _require_legacy_backend()
    _require_admin(request)
    return kg_graph.build_all(body.cluster_id)
