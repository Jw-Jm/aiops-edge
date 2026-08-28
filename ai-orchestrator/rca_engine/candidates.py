from __future__ import annotations
from collections import defaultdict, deque
from typing import Any


def graph_candidates(entity: dict[str, Any], graph_client: Any, *, max_depth: int = 6) -> dict[str, Any]:
    """Ask query-api for the bounded propagation candidate subgraph."""
    uid = str(entity.get("entity_uid") or "")
    return graph_client(graph_operation="candidate_subgraph", entity_uid=uid,
                        relation_policy="root_cause_candidate_v1", max_depth=min(max_depth, 6),
                        max_vertices=2000, max_edges=5000)


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
        if source not in vertices or target not in vertices or edge.get("propagates_failure", True) is False:
            continue
        direction = str(edge.get("candidate_direction") or "OUT").upper()
        if direction in {"OUT", "BOTH", ""}:
            adjacency[source].append((target, edge))
        if direction in {"IN", "BOTH"}:
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
