from dataclasses import dataclass, field
from typing import Optional
from collections import deque
from .noderegistry import node_registry


@dataclass
class GraphNode:
    id: str
    type: str
    name: str = ""
    config: dict = field(default_factory=dict)
    position: dict = field(default_factory=dict)


@dataclass
class GraphEdge:
    id: str = ""
    source: str = ""
    source_port: str = "next"
    target: str = ""


@dataclass
class Graph:
    nodes: list = field(default_factory=list)
    edges: list = field(default_factory=list)


def graph_from_dict(data: dict) -> Graph:
    nodes = [GraphNode(**{k: v for k, v in n.items() if k in
                          ("id", "type", "name", "config", "position")})
             for n in data.get("nodes", [])]
    edges = []
    for i, e in enumerate(data.get("edges", [])):
        edges.append(GraphEdge(
            id=e.get("id", f"e{i}"),
            source=e.get("source", ""),
            source_port=e.get("sourcePort", e.get("source_port", "next")),
            target=e.get("target", ""),
        ))
    return Graph(nodes=nodes, edges=edges)


def graph_to_dict(g: Graph) -> dict:
    return {
        "nodes": [{"id": n.id, "type": n.type, "name": n.name,
                   "config": n.config, "position": n.position} for n in g.nodes],
        "edges": [{"id": e.id, "source": e.source, "sourcePort": e.source_port,
                   "target": e.target} for e in g.edges],
    }


def validate_graph(g: Graph):
    if not g.nodes:
        raise ValueError("graph has no nodes")
    # 1. 未知节点类型
    for n in g.nodes:
        if node_registry.lookup(n.type) is None:
            raise ValueError(f"unknown node type: {n.type}")
    ids = {n.id for n in g.nodes}
    # 2. 边引用存在节点
    for e in g.edges:
        if e.source not in ids:
            raise ValueError(f"edge source missing node: {e.source}")
        if e.target not in ids:
            raise ValueError(f"edge target missing node: {e.target}")
        spec = node_registry.lookup({n.id: n for n in g.nodes}[e.source].type)
        valid_ports = set(spec.ports) | {"error"}
        if e.source_port not in valid_ports:
            raise ValueError(f"invalid source port {e.source_port} for node {e.source}")
    # 3. Kahn 环检测
    adj = {n.id: [] for n in g.nodes}
    indeg = {n.id: 0 for n in g.nodes}
    for e in g.edges:
        adj[e.source].append(e.target)
        indeg[e.target] += 1
    q = deque([nid for nid, d in indeg.items() if d == 0])
    visited = 0
    while q:
        cur = q.popleft()
        visited += 1
        for nxt in adj[cur]:
            indeg[nxt] -= 1
            if indeg[nxt] == 0:
                q.append(nxt)
    if visited != len(g.nodes):
        raise ValueError("cycle detected in graph")
