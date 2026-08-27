from __future__ import annotations
from typing import Any


def resolve_entity(request: Any, graph_client: Any) -> dict[str, Any] | None:
    """Resolve by canonical UID first, then bounded alias search via query-api."""
    uid = str(getattr(request, "entity_uid", "") or getattr(request, "resource_id", "") or "").strip()
    if uid:
        response = graph_client(graph_operation="get_vertex", entity_uid=uid)
        if isinstance(response, dict):
            return response.get("entity") or response.get("vertex") or response
    name = str(getattr(request, "entity_name", "") or "").strip()
    if not name:
        return None
    response = graph_client(graph_operation="search_entities", name=name, entity_type="service", limit=20)
    if not isinstance(response, dict):
        return None
    entities = response.get("entities") or response.get("vertices") or []
    return entities[0] if len(entities) == 1 else None
