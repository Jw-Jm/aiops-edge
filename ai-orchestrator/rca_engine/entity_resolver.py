from __future__ import annotations
from typing import Any


_TARGET_TO_GRAPH_TYPE = {
    "node": "k8s_node",
    "service": "service",
    "deployment": "deployment",
    "statefulset": "statefulset",
    "daemonset": "daemonset",
    "pod": "pod",
    "container": "container",
    "namespace": "namespace",
    "vm": "vm",
    "alert": "alert",
    "trace": "trace",
}


def resolve_entity(request: Any, graph_client: Any) -> dict[str, Any] | None:
    """Resolve by canonical UID first, then bounded alias search via query-api."""
    uid = str(getattr(request, "entity_uid", "") or getattr(request, "resource_id", "") or "").strip()
    # Human resource names (``orbstack``, ``checkout``) are aliases, not graph
    # UIDs.  Avoid spending an audited failed get_vertex ToolRun on those
    # names; canonical graph identities carry a kind/version delimiter.
    if uid and ":" in uid:
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
    # A service-scoped lookup can legitimately return ``GRAPH_ENTITY_NOT_FOUND``
    # when the human target is a node/workload/middleware object.  That is not
    # a graph outage: continue to the ontology-wide, still uniquely bounded
    # resolver below.  Other client failures are also handled as a failed
    # first attempt so the final result can report a truthful unresolved
    # entity instead of short-circuiting before the fallback.
    target_type = str(getattr(request, "target_type", "service") or "service").strip().lower()
    preferred_type = _TARGET_TO_GRAPH_TYPE.get(target_type, "service")
    try:
        response = graph_client(graph_operation="resolve_entity", name=name, entity_type=preferred_type)
    except Exception:
        response = {}
    if not isinstance(response, dict):
        response = {}
    entity = response.get("entity") or response.get("vertex")
    if isinstance(entity, dict):
        return entity
    entities = response.get("entities") or response.get("vertices") or response.get("items") or []
    if len(entities) == 1 and isinstance(entities[0], dict):
        return entities[0]

    # A human target is not always a service: Kubernetes nodes, workloads and
    # middleware are valid RCA entities too.  The service-scoped lookup above
    # remains first (and therefore deterministic); only an empty/ambiguous
    # service result may fall back to the server-side ontology search without
    # an entity_type.  We still require exactly one canonical match, so this
    # cannot silently pick a cross-domain or cross-cluster object.
    try:
        response = graph_client(graph_operation="resolve_entity", name=name, entity_type="")
    except Exception:
        return None
    if not isinstance(response, dict):
        return None
    entity = response.get("entity") or response.get("vertex")
    if isinstance(entity, dict):
        return entity
    entities = response.get("entities") or response.get("vertices") or response.get("items") or []
    return entities[0] if len(entities) == 1 and isinstance(entities[0], dict) else None
