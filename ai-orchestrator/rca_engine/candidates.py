from __future__ import annotations
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
