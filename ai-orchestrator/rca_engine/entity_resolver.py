from __future__ import annotations
from typing import Any


def resolve_entity(request: Any, graph_client: Any) -> dict[str, Any] | None:
    """Resolve by canonical UID first, then bounded alias search via query-api."""
    uid = str(getattr(request, "entity_uid", "") or getattr(request, "resource_id", "") or "").strip()
    if uid:
        try:
            response = graph_client(graph_operation="get_vertex", entity_uid=uid)
            if isinstance(response, dict):
                entity = response.get("entity") or response.get("vertex") or response
                if isinstance(entity, dict):
                    return entity
        except Exception:
            # A human-facing service name is not a canonical UID. Fall back to
            # the query-api alias resolver instead of masking it as an outage.
            pass
    name = str(getattr(request, "entity_name", "") or "").strip()
    if not name:
        return None
    # The query-api graph boundary exposes one canonical resolver operation;
    # callers must not invent a second search endpoint or perform name joins
    # against a legacy topology table.
    response = graph_client(graph_operation="resolve_entity", name=name, entity_type="service")
    if not isinstance(response, dict):
        return None
    entity = response.get("entity") or response.get("vertex")
    if isinstance(entity, dict):
        return entity
    entities = response.get("entities") or response.get("vertices") or []
    return entities[0] if len(entities) == 1 else None
