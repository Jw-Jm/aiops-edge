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

from dataclasses import dataclass
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
        )

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
    def run_id(self) -> str:
        return str(getattr(self._ctx, "run_id", ""))

    @property
    def nonce(self) -> str:
        return str(getattr(self._ctx, "nonce", ""))
