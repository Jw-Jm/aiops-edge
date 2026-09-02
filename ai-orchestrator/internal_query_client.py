"""P7.2 InternalQueryClient — V9.3 Phase7 orchestrator 获取事实的唯一通道。

唯一事实路径：orchestrator → TrustedRequestContext V2 → query-api /internal/v1/query/*。
禁止任何 direct DB / ClickHouse / VictoriaMetrics / VictoriaLogs / Kubernetes 旁路。

安全 gate（按顺序）：
1. tool_id 必须已在 Tool Registry 注册且 active，且非执行类（execution_state != disabled）。
2. capability 只能来自 ToolDefinition（Tool Registry），调用方不能传入 capability ——
   LLM/Agent 无法生成或篡改权限字符串。
3. operation → 固定 /internal/v1/query/<op> 端点；Tool 的 capability 必须与该端点所需
   capability 精确一致（Tool-Capability binding，T6）。
4. params 只接受结构化白名单字段；禁止 sql / promql 等 backend query language。
5. 每次调用签发唯一 TrustedRequestContext V2（非复用）。
6. HTTP 语义不降级：403 permission_denied ≠ 200 NO_DATA；503 unavailable 不降级为 healthy。
"""
from __future__ import annotations

import json
import os
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, Mapping, Optional

from tool_registry import ToolRegistry
from trusted_context import TrustedContextError, sign_trusted_request_context_v2
from trusted_context_issuer import TrustedContextIssuer
from tool_execution_context import ToolExecutionContext


# operation → (固定 internal endpoint, 该端点所需 capability) 对齐 query-api routeCapability。
OPERATION_ROUTES: Dict[str, tuple] = {
    "metrics": ("/internal/v1/query/metrics", "observability.metrics.read"),
    "logs": ("/internal/v1/query/logs", "observability.logs.read"),
    "traces": ("/internal/v1/query/traces", "observability.traces.read"),
    "alerts": ("/internal/v1/query/alerts", "observability.alerts.read"),
    "events": ("/internal/v1/query/events", "kubernetes.events.read"),
    "topology": ("/internal/v1/query/topology", "observability.topology.read"),
    "middleware": ("/internal/v1/query/topology/middleware", "observability.topology.read"),
    "kubernetes": ("/internal/v1/query/kubernetes", "kubernetes.resources.read"),
    "changes": ("/internal/v1/query/changes", "changes.read"),
    "knowledge": ("/internal/v1/query/knowledge", "knowledge.search"),
    "graph": ("/internal/v1/query/graph", "knowledge.graph.read"),
    "kubevirt": ("/internal/v1/query/kubevirt", "kubevirt.resources.read"),
    "hardware_inventory": ("/internal/v1/query/hardware/inventory", "hardware.inventory.read"),
    "hardware_health": ("/internal/v1/query/hardware/health", "hardware.health.read"),
    "catalog": ("/internal/v1/query/catalog", "catalog.read"),
    "network_topology": ("/internal/v1/query/network-topology", "network.topology.read"),
}

# internalQueryRequest 结构化字段白名单（禁止 backend query language）。
_ALLOWED_PARAM_KEYS = frozenset(
    {
        "service", "services", "query", "since", "minutes", "hours", "namespace", "limit", "offset", "top_k",
        "graph_operation", "entity_uid", "target_entity_uid", "entity_type", "name", "direction",
        "relation_types", "relation_policy", "max_depth", "max_vertices", "max_edges", "include_stale", "cursor",
        "context_version",
    }
)

_DEFAULT_TIMEOUT = 10


@dataclass
class QueryResult:
    """一次 /internal/v1/query/* 调用的成功结果。"""

    http_status: int
    body: Dict[str, Any]


class InternalQueryError(TrustedContextError):
    """internal query 边界的结构化错误，保留 HTTP 语义不降级。"""

    def __init__(self, kind: str, http_status: int, message: str):
        self.kind = kind
        self.http_status = http_status
        super().__init__(kind)


def _default_http(
    path: str,
    *,
    context_claims: Mapping[str, Any],
    method: str = "POST",
    data: Optional[bytes] = None,
    headers: Optional[Mapping[str, str]] = None,
) -> tuple:
    """默认 HTTP transport：把 claims 签成 EdDSA JWS 并发 POST 到 query-api。

    复用 internal_query 的 URL 校验与私钥加载；失败返回 (status, body) 而非抛异常，
    使 InternalQueryClient 能按 HTTP 语义区分 permission_denied / unavailable / no_data。
    """
    import urllib.error
    import urllib.request

    from internal_query import _load_private_key, _validate_query_api_url
    from mtls import urlopen as mtls_urlopen

    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    token = sign_trusted_request_context_v2(dict(context_claims), private_key)
    service_token = os.environ.get("INTERNAL_TOKEN", "")
    if not service_token:
        raise TrustedContextError("invalid_service")
    base = os.environ.get("QUERY_API_URL", "").rstrip("/")
    # URL 接线修复（真实环境验证 Phase A 发现）：QUERY_API_URL 常含路径前缀（如 /api/v1），
    # 而 query-api 的 internal 路由注册在 /internal/v1/query/*（根路径，无 /api/v1）。
    # 若直接 base + path 会得到 .../api/v1/internal/v1/query/* → 403（AuthMiddleware 对
    # /api/v1/* 走 canonical 路由检查，internal 前缀不匹配）。因此剥掉 base 的路径部分，
    # 只保留 scheme://host:port，使 internal 路由落在根路径。
    from urllib.parse import urlparse
    parsed = urlparse(base)
    origin = f"{parsed.scheme}://{parsed.netloc}"
    url = origin + path
    _validate_query_api_url(url)
    request_headers = {
        **(dict(headers) if headers else {}),
        "X-Internal-Token": service_token,
        "X-Trusted-Request-Context": token,
    }
    request = urllib.request.Request(url, data=data, method=method.upper(), headers=request_headers)
    try:
        with mtls_urlopen(request, timeout=_DEFAULT_TIMEOUT) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as e:
        try:
            body = e.read()
        except Exception:
            body = b"{}"
        return e.code, body


class InternalQueryClient:
    """orchestrator 侧唯一事实访问通道，封装 Tool gate + 能力签发 + /internal/v1/query/*。"""

    def __init__(
        self,
        *,
        issuer: TrustedContextIssuer,
        registry: Optional[ToolRegistry] = None,
        http: Optional[Callable] = None,
    ) -> None:
        self._issuer = issuer
        self._registry = registry or ToolRegistry
        self._http = http or _default_http

    def query(
        self,
        *,
        tool_id: str,
        operation: str,
        tenant_id: str,
        cluster_id: str,
        params: Mapping[str, Any],
        context_ref: str,
        execution_context: ToolExecutionContext | None = None,
    ) -> QueryResult:
        """执行一次带能力门控的 internal query。capability 不能由调用方传入。"""
        tool = self._resolve_tool(tool_id)
        route = OPERATION_ROUTES.get(operation)
        if route is None:
            raise TrustedContextError("invalid_context")
        endpoint, required_capability = route
        # Tool-Capability binding：Tool 的能力必须与该端点所需能力精确一致（T6）。
        if tool.capability != required_capability:
            raise TrustedContextError("invalid_context")
        raw_params = dict(params)
        raw_params.pop("_execution_context", None)
        body = self._build_body(raw_params)
        tool_context = execution_context
        if tool_context is None and isinstance(params, Mapping) and context_ref:
            # Investigation and Chat callers pass identity in the trusted
            # in-process context; Chat uses the durable ChatTool envelope.
            context_mapping = params.get("_execution_context") if isinstance(params.get("_execution_context"), Mapping) else None
            if context_mapping and context_mapping.get("workload_kind") in {"investigation", "chat"}:
                tool_context = ToolExecutionContext.from_mapping(
                    context_mapping, tool_id=tool_id, params=raw_params,
                )
        principal_type = "system"
        principal_id = self._principal_id(context_ref)
        session_id = None
        if tool_context is not None and tool_context.workload_kind == "investigation":
            body.update(tool_context.to_body())
            run_id = tool_context.run_id
            workload_kind = tool_context.workload_kind
        elif tool_context is not None and tool_context.workload_kind == "chat":
            if tool_context.tenant_id != str(tenant_id) or tool_context.cluster_id != str(cluster_id):
                raise TrustedContextError("invalid_context")
            body.update(tool_context.to_body())
            # Chat has no Investigation Run.  The signed run_id is only a
            # short-lived correlation value required by the wire contract; it
            # must never be used to create ai_runs/ai_tool_runs.
            run_id = self._run_id(f"chat::{context_ref}")
            workload_kind = "chat"
            principal_type = tool_context.principal_type
            principal_id = tool_context.principal_id
            session_id = tool_context.session_id
        else:
            run_id = self._run_id(context_ref)
            workload_kind = "platform"
        claims = self._issuer.build_claims(
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            capability=tool.capability,
            run_id=run_id,
            principal_type=principal_type,
            principal_id=principal_id,
            session_id=session_id,
            workload_kind=workload_kind,
        )
        headers = {"Content-Type": "application/json", "X-Context-Ref": context_ref}
        status, raw = self._http(
            endpoint,
            context_claims=claims,
            method="POST",
            data=json.dumps(body, separators=(",", ":")).encode("utf-8"),
            headers=headers,
        )
        return self._normalize(status, raw)

    def query_graph_v1(
        self,
        *,
        tenant_id: str,
        cluster_id: str,
        params: Mapping[str, Any],
        context_ref: str,
        execution_context: ToolExecutionContext | None = None,
    ) -> QueryResult:
        """Typed graph query entrypoint; callers cannot select another backend."""
        return self.query(
            tool_id="query_graph.v1", operation="graph", tenant_id=tenant_id,
            cluster_id=cluster_id, params=params, context_ref=context_ref,
            execution_context=execution_context,
        )

    def _resolve_tool(self, tool_id: str):
        tool = self._registry.get(tool_id)
        if tool is None:
            raise TrustedContextError("invalid_context")
        if tool.lifecycle_status != "active":
            raise TrustedContextError("invalid_context")
        if tool.execution_state == "disabled":
            raise TrustedContextError("invalid_context")
        return tool

    def _build_body(self, params: Mapping[str, Any]) -> dict:
        if not isinstance(params, Mapping):
            raise TrustedContextError("invalid_context")
        body: dict = {}
        for key, value in params.items():
            if key not in _ALLOWED_PARAM_KEYS:
                # 拒绝 sql / promql 等 backend query language。
                raise TrustedContextError("invalid_context")
            body[key] = value
        return body

    @staticmethod
    def _run_id(context_ref: str) -> str:
        # 从 context_ref 稳定派生 run_id，保证同一调用链可追踪、不同 context 隔离。
        return str(uuid.uuid5(uuid.NAMESPACE_URL, f"run::{context_ref}"))

    @staticmethod
    def _principal_id(context_ref: str) -> str:
        return str(uuid.uuid5(uuid.NAMESPACE_URL, f"principal::{context_ref}"))

    @staticmethod
    def _normalize(status: int, raw: bytes) -> QueryResult:
        try:
            body = json.loads(raw.decode("utf-8") if isinstance(raw, bytes) else raw) if raw else {}
        except Exception:
            body = {}
        if not isinstance(body, dict):
            body = {}
        if status == 200:
            # 200 + NO_DATA 是合法空结果（no_data ≠ permission_denied），不降级。
            return QueryResult(http_status=200, body=body)
        kind = {
            401: "service_auth_failed",
            403: "permission_denied",
            404: "not_found",
            409: "scope_mismatch",
            422: "validation_failed",
            503: "unavailable",
            504: "timeout",
        }.get(status, "internal")
        raise InternalQueryError(kind=kind, http_status=status, message=str(body.get("message", "")))
