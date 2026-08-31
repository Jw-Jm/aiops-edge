from __future__ import annotations
from collections import defaultdict, deque
import os
from typing import Any


def _bounded_limit(name: str, default: int, upper: int) -> int:
    """Read an operator limit without allowing an unbounded graph request."""
    try:
        value = int(os.environ.get(name, str(default)))
    except (TypeError, ValueError):
        value = default
    return max(1, min(value, upper))


def graph_candidates(entity: dict[str, Any], graph_client: Any, *, max_depth: int = 6) -> dict[str, Any]:
    """Ask query-api for a bounded propagation candidate subgraph.

    The RCA path starts with a conservative one-hop envelope (50 vertices,
    150 edges).  A high-degree Kubernetes node can make HugeGraph spend most
    of its budget expanding the frontier before its result limit is applied;
    the old 6/2000/5000 request therefore turned a bounded API into a timeout
    source.  Operators may raise the values only after a capacity gate, but
    every value remains capped locally before it reaches the signed Query API
    boundary.
    """
    uid = str(entity.get("entity_uid") or "")
    depth_cap = _bounded_limit("RCA_GRAPH_MAX_DEPTH", 1, 6)
    vertex_cap = _bounded_limit("RCA_GRAPH_MAX_VERTICES", 50, 500)
    edge_cap = _bounded_limit("RCA_GRAPH_MAX_EDGES", 150, 1500)
    return graph_client(graph_operation="candidate_subgraph", entity_uid=uid,
                        relation_policy="root_cause_candidate_v1", max_depth=min(max_depth, depth_cap),
                        max_vertices=vertex_cap, max_edges=edge_cap)


def candidate_rows(subgraph: dict[str, Any]) -> list[dict[str, Any]]:
    vertices = subgraph.get("vertices") if isinstance(subgraph, dict) else []
    return list(vertices or [])


def propagation_paths(subgraph: dict[str, Any], root_uid: str, symptom_uid: str, *, max_paths: int = 5) -> list[dict[str, Any]]:
    """Return bounded, directed root-cause-to-symptom paths.

    The candidate subgraph is a search space, not a propagation explanation.
    Only edges marked as failure-propagating are traversed, and each returned
    item contains the exact vertices and edges used by that path.
    """
    root_uid, symptom_uid = str(root_uid or ""), str(symptom_uid or "")
    if not root_uid or not symptom_uid:
        return []
    vertices = {str(vertex.get("entity_uid")): vertex for vertex in (subgraph.get("vertices") or []) if vertex.get("entity_uid")}
    if root_uid not in vertices or symptom_uid not in vertices:
        return []
    if root_uid == symptom_uid:
        return [{"root_cause_uid": root_uid, "symptom_uid": symptom_uid, "vertex_uids": [root_uid],
                 "edge_uids": [], "vertices": [vertices[root_uid]], "edges": []}]

    adjacency: dict[str, list[tuple[str, dict[str, Any]]]] = defaultdict(list)
    for edge in subgraph.get("edges") or []:
        source = str(edge.get("source_uid") or "")
        target = str(edge.get("target_uid") or "")
        # A path is an explanation, not a copy of the candidate search space.
        # Production graph edges carry this flag explicitly; missing values
        # are excluded so an incomplete projection cannot fabricate a path.
        if source not in vertices or target not in vertices or edge.get("propagates_failure") is not True:
            continue
        try:
            confidence = float(edge.get("confidence", 1.0))
        except (TypeError, ValueError):
            confidence = 0.0
        if confidence < 0.8:
            continue
        # candidate_direction describes the traversal from symptom to a
        # possible cause.  The explanation must be emitted in the opposite
        # direction (root cause -> symptom), hence OUT becomes target->source
        # and IN becomes source->target.
        direction = str(edge.get("candidate_direction") or "").upper()
        if direction == "OUT":
            adjacency[target].append((source, edge))
        elif direction == "IN":
            adjacency[source].append((target, edge))
        elif direction == "BOTH":
            adjacency[source].append((target, edge))
            adjacency[target].append((source, edge))
    for values in adjacency.values():
        values.sort(key=lambda item: (item[0], str(item[1].get("edge_uid") or "")))

    paths: list[dict[str, Any]] = []
    queue: deque[tuple[str, list[str], list[dict[str, Any]]]] = deque([(root_uid, [root_uid], [])])
    while queue and len(paths) < max_paths:
        current, vertex_uids, path_edges = queue.popleft()
        for target, edge in adjacency.get(current, []):
            if target in vertex_uids:
                continue
            next_vertices = [*vertex_uids, target]
            next_edges = [*path_edges, edge]
            if target == symptom_uid:
                paths.append({"root_cause_uid": root_uid, "symptom_uid": symptom_uid,
                              "vertex_uids": next_vertices,
                              "edge_uids": [str(item.get("edge_uid") or "") for item in next_edges],
                              "vertices": [vertices[uid] for uid in next_vertices], "edges": next_edges})
                if len(paths) >= max_paths:
                    break
            elif len(next_vertices) < 20:
                queue.append((target, next_vertices, next_edges))
    return paths
