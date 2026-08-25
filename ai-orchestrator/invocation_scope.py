"""Internal business-scope adapters (P3.9-B1).

These types decouple the trusted ingress Envelope (RunInvocationContext) from the
legacy internal business code without reintroducing the single legacy
``contract.RequestContext`` as the internal trust object.

Rules:
- ``InvocationScope`` is a pure in-process business parameter: it is NOT signed,
  serialized, derived from headers/JWT, or used as a cross-service contract.
- ``ScopeView`` is the Protocol the old business functions depend on for
  tenant_id / cluster_id / principal_id / session_id / request_id.
- ``LegacyScopeAdapter`` exists only as a Phase 3-13 transition adapter for the
  old AI Chat path; it is never used by the new RunInvocation ingress.
"""

from __future__ import annotations

from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass, replace
from typing import Optional, Protocol, runtime_checkable

from trusted_context import TrustedContextError


class ValidationFailed(ValueError):
    """Stable internal error for scope validation failures (e.g. multi-cluster
    legacy downstream)."""

    error_code = "VALIDATION_FAILED"


@runtime_checkable
class ScopeView(Protocol):
    """The subset of fields old business code actually needs from any scope."""

    @property
    def principal_id(self) -> str: ...

    @property
    def session_id(self) -> Optional[str]: ...

    @property
    def tenant_id(self) -> str: ...

    @property
    def cluster_id(self) -> str: ...

    @property
    def request_id(self) -> str: ...

    @property
    def source(self) -> str: ...


@dataclass(frozen=True)
class InvocationScope:
    """Pure internal business scope derived from a verified RunInvocationContext.

    Exactly one cluster is required for the legacy downstream adapter. Multi-cluster
    RunInvocation contexts are refused (VALIDATION_FAILED) rather than picking the
    first cluster.
    """

    principal_type: str
    principal_id: str
    session_id: Optional[str]
    tenant_id: str
    cluster_id: str
    request_id: str
    source: str
    run_id: str = ""
    invocation_id: str = ""
    workload_kind: str = "chat"
    capability: str = ""
    executor_id: str = ""
    lease_epoch: int = 0
    lease_token: str = ""

    @classmethod
    def from_run_invocation_context(cls, claims: dict) -> "InvocationScope":
        """Build an InvocationScope from a verified RunInvocationContext payload.

        Only the canonical cluster scope is accepted; multi-cluster is refused.
        This is the ONLY way to construct an InvocationScope for the new ingress.
        """
        cluster_scope = claims.get("cluster_scope") or []
        if not isinstance(cluster_scope, list) or len(cluster_scope) != 1:
            raise ValidationFailed("run-invocation legacy downstream requires exactly one cluster")
        principal_id = claims.get("principal_id")
        tenant_id = claims.get("tenant_id")
        request_id = claims.get("request_id")
        if not principal_id or not tenant_id or not request_id:
            raise TrustedContextError("invalid_context")
        return cls(
            principal_type=claims.get("principal_type", "user"),
            principal_id=str(principal_id),
            session_id=(str(claims["session_id"]) if claims.get("session_id") else None),
            tenant_id=str(tenant_id),
            cluster_id=str(cluster_scope[0]),
            request_id=str(request_id),
            source=str(claims.get("source", "orchestrator")),
            run_id=str(claims.get("run_id") or ""),
            invocation_id=str(claims.get("invocation_id") or ""),
            workload_kind=("investigation" if claims.get("capability") == "ai.investigate" else "chat"),
            capability=str(claims.get("capability") or ""),
        )

    def bind_lease(self, *, executor_id: str, lease_epoch: int, lease_token: str) -> "InvocationScope":
        return replace(self, executor_id=executor_id, lease_epoch=int(lease_epoch), lease_token=lease_token)

    # ScopeView properties (makes InvocationScope satisfy ScopeView directly).
    @property
    def _scope_view(self) -> ScopeView:
        return self


class LegacyScopeAdapter:
    """Transition adapter: legacy single ``RequestContext`` → ScopeView.

    Phase 3-13 only, for the old AI Chat path. It never signs/verifies,
    never parses headers, and is never used by the RunInvocation ingress.
    It is removed with the old AI Chat path in Phase 14.
    """

    def __init__(self, legacy_context):
        # legacy_context is the orchestrator's internal RequestContext model.
        self._ctx = legacy_context

    @property
    def principal_id(self) -> str:
        return str(getattr(self._ctx, "user_id", ""))

    @property
    def session_id(self) -> Optional[str]:
        # Return the underlying typed value (e.g. UUID) to match legacy callers;
        # consumers that need a string normalize it via str().
        return getattr(self._ctx, "session_id", None)

    @property
    def tenant_id(self) -> str:
        return getattr(self._ctx, "tenant_id", "")

    @property
    def cluster_id(self) -> str:
        return getattr(self._ctx, "cluster_id", "")

    @property
    def request_id(self) -> str:
        return getattr(self._ctx, "request_id", "")

    @property
    def source(self) -> str:
        return str(getattr(self._ctx, "source", "planner"))

    # Legacy-compatible aliases so existing callers that read user_id/capability
    # keep working during the Phase 3-13 transition. Not part of ScopeView.
    # Return the underlying typed values (e.g. UUID) to match legacy assertions.
    @property
    def user_id(self):
        return getattr(self._ctx, "user_id", "")

    @property
    def capability(self) -> str:
        return str(getattr(self._ctx, "capability", ""))

    @property
    def workload_kind(self) -> str:
        return "chat"

    @property
    def run_id(self) -> str:
        return str(getattr(self._ctx, "run_id", ""))

    @property
    def nonce(self) -> str:
        return str(getattr(self._ctx, "nonce", ""))


# ── P-fix: ScopeView 可序列化投影 ───────────────────────────────────────────
# LangGraph 的 checkpoint saver（msgpack/JsonPlus）无法序列化 ScopeView 实例
# （InvocationScope / LegacyScopeAdapter 是 dataclass / 包装对象）。而
# request_context 只作为只读上下文传给下游工具，不应被持久化。为避免真实环境
# 启用持久化 saver 时 astream/ainvoke 因序列化崩溃，state 中的 request_context
# 存「可 msgpack 序列化的纯 dict 投影」，节点读取时用 ScopeViewSnapshot 还原为
# 满足 @runtime_checkable ScopeView 协议的对象。
_SNAPSHOT_FIELDS = (
    "principal_id", "session_id", "tenant_id", "cluster_id",
    "request_id", "source", "run_id", "invocation_id", "principal_type", "capability",
    "workload_kind", "executor_id", "lease_epoch",
)

# Lease tokens are short-lived secrets.  They are bound to the worker task while
# a graph is executing, but are deliberately excluded from checkpoint state.
_LEASE_TOKEN: ContextVar[str] = ContextVar("aiops_investigation_lease_token", default="")


@contextmanager
def bind_execution_lease_token(token: str):
    """Bind a lease token for the current worker task without checkpointing it."""
    marker = _LEASE_TOKEN.set(str(token or ""))
    try:
        yield
    finally:
        _LEASE_TOKEN.reset(marker)


def current_execution_lease_token() -> str:
    return _LEASE_TOKEN.get()


class ScopeViewSnapshot:
    """满足 ``ScopeView`` 协议的可还原轻量对象 + 纯 dict 投影工厂。

    同时满足 :func:`internal_query._validated_source_context` 读取的全部字段
    （principal_id/session_id/tenant_id/cluster_id/run_id/principal_type/
    capability/source），缺失字段用 ``getattr(..., "")`` 兜底为空，与
    ``LegacyScopeAdapter`` 的空值语义一致。
    """

    __slots__ = _SNAPSHOT_FIELDS

    def __init__(self, principal_id="", session_id=None, tenant_id="", cluster_id="",
                 request_id="", source="", run_id="", invocation_id="", principal_type="user",
                 capability="", workload_kind="chat", executor_id="", lease_epoch=0):
        self.principal_id = str(principal_id)
        self.session_id = str(session_id) if session_id is not None else None
        self.tenant_id = str(tenant_id)
        self.cluster_id = str(cluster_id)
        self.request_id = str(request_id)
        self.source = str(source)
        self.run_id = str(run_id)
        self.invocation_id = str(invocation_id)
        self.principal_type = str(principal_type)
        self.capability = str(capability)
        self.workload_kind = str(workload_kind or "chat")
        self.executor_id = str(executor_id or "")
        self.lease_epoch = int(lease_epoch or 0)

    @classmethod
    def to_projection(cls, view: ScopeView | None) -> dict:
        """把任意 ScopeView（或 None）转成可 msgpack 序列化的纯 dict 投影。"""
        if view is None:
            return {}
        proj = {}
        for f in _SNAPSHOT_FIELDS:
            v = getattr(view, f, None)
            if f == "lease_epoch":
                proj[f] = int(v or 0)
            else:
                proj[f] = (str(v) if v is not None else None)
        # session_id 为空时规范为 None，与下游语义一致
        if not proj.get("session_id"):
            proj["session_id"] = None
        return proj

    @classmethod
    def from_projection(cls, data: dict | ScopeView | None) -> ScopeView:
        """从纯 dict 投影（或已有 ScopeView）还原为满足 ScopeView 协议的对象。

        兼容: 传入已是 ScopeView 实例时原样返回（避免二次包装）；传入 None /
        空 dict 时返回一个空 snapshot（cluster_id="" 等，下游 fail-closed）。"""
        if isinstance(data, ScopeView):
            return data
        data = data or {}
        return cls(
            principal_id=data.get("principal_id") or "",
            session_id=data.get("session_id") or None,
            tenant_id=data.get("tenant_id") or "",
            cluster_id=data.get("cluster_id") or "",
            request_id=data.get("request_id") or "",
            source=data.get("source") or "",
            run_id=data.get("run_id") or "",
            invocation_id=data.get("invocation_id") or "",
            principal_type=data.get("principal_type") or "user",
            capability=data.get("capability") or "",
            workload_kind=data.get("workload_kind") or "chat",
            executor_id=data.get("executor_id") or "",
            lease_epoch=data.get("lease_epoch") or 0,
        )
