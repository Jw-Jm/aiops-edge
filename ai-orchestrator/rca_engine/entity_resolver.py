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


def _is_entity_not_found(exc: BaseException) -> bool:
    """D16b：区分"实体未建模"（数据事实）与"图谱系统故障"。

    查询边界的失败信封被转成异常：内部查询客户端的 ``InternalQueryError``
    （kind=not_found）与适配器层的 ``RuntimeError(GRAPH_ENTITY_NOT_FOUND)``。
    实体不存在以这两类错误码表达；其它（连接拒绝、边界拒绝、超时）属于
    系统故障，必须上抛，让引擎 fail-closed 而不是伪装成"实体未找到"。
    """
    candidates = [str(getattr(exc, "args", ("",))[0]) if getattr(exc, "args", None) else ""]
    candidates.append(str(getattr(exc, "kind", "")))
    candidates.append(str(getattr(exc, "message", "")))
    text = " ".join(candidates)
    return ("GRAPH_ENTITY_NOT_FOUND" in text or "ENTITY_AMBIGUOUS" in text
            or "not_found" in text)


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
        except Exception as exc:
            # 实体不存在（canonical UID 无对应顶点）→ 回退名称路径；
            # 系统故障（连接/边界拒绝）→ 上抛，fail-closed。
            if not _is_entity_not_found(exc):
                raise
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
    except Exception as exc:
        if not _is_entity_not_found(exc):
            raise  # 系统故障必须上抛，不得伪装成"实体未找到"
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
    except Exception as exc:
        if not _is_entity_not_found(exc):
            raise
        return None
    if not isinstance(response, dict):
        return None
    entity = response.get("entity") or response.get("vertex")
    if isinstance(entity, dict):
        return entity
    entities = response.get("entities") or response.get("vertices") or response.get("items") or []
    return entities[0] if len(entities) == 1 and isinstance(entities[0], dict) else None
