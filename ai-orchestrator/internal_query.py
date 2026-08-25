"""Fail-closed orchestrator calls to the query-api trust boundary (V9.2)."""

from __future__ import annotations

import base64
import binascii
import os
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone
from typing import Mapping
from uuid import uuid4

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from contracts import TrustedRequestContext
from invocation_scope import ScopeView
from trusted_context import TrustedContextError, sign_trusted_request_context_v2


_DEFAULT_TIMEOUT = 10
_CONTEXT_LIFETIME = timedelta(seconds=30)
_ISSUER = "ai-orchestrator"
_AUDIENCE = "ai-apm-query-go"
_FORBIDDEN_HEADERS = frozenset(
    {
        "authorization",
        "credential-ref",
        "x-cluster-id",
        "x-credential-ref",
        "x-tenant-id",
        "x-trusted-request-context",
    }
)


def signed_query_api_request(
    url: str,
    *,
    context: RequestContext | TrustedRequestContext | dict,
    method: str = "GET",
    data: bytes | None = None,
    headers: Mapping[str, str] | None = None,
    timeout: int | float = _DEFAULT_TIMEOUT,
) -> bytes:
    """Call query-api with separate service and signed-context credentials.

    ``context`` must be the explicit, already-authorized context received by the
    orchestrator. The helper preserves its authorization scope while minting a
    fresh V9.2 TrustedRequestContext (EdDSA, typ=AIOPS-CONTEXT) for this call.

    P3.9 transition: source context may be a legacy single ``RequestContext`` or
    the V9.2 ``TrustedRequestContext``. After cutover only ``TrustedRequestContext``
    is accepted and the legacy branch is removed.
    """
    now = datetime.now(timezone.utc)
    source = _validated_source_context(context, now)
    service_token = os.environ.get("INTERNAL_TOKEN", "")
    if not service_token:
        raise TrustedContextError("invalid_service")
    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    _validate_query_api_url(url)

    caller_headers = dict(headers or {})
    for name, value in caller_headers.items():
        normalized = name.strip().lower()
        if (
            not normalized
            or normalized in _FORBIDDEN_HEADERS
            or normalized.startswith("x-internal-")
            or not isinstance(value, str)
        ):
            raise TrustedContextError("invalid_context")

    delegated_claims = {
        "version": 1,
        "context_type": "trusted_request",
        "issuer": _ISSUER,
        "audience": _AUDIENCE,
        "request_id": uuid4(),
        "run_id": source.get("run_id"),
        "principal_type": source.get("principal_type", "user"),
        "principal_id": source.get("principal_id"),
        "session_id": source.get("session_id"),
        "tenant_id": source["tenant_id"],
        "scope_kind": "cluster",
        "cluster_id": source.get("cluster_id"),
        "capability": source.get("capability"),
        "source": source.get("source", "planner"),
        "workload_kind": source.get("workload_kind", "platform"),
        "issued_at": now,
        "expires_at": now + _CONTEXT_LIFETIME,
        "nonce": uuid4(),
    }
    signed_context = sign_trusted_request_context_v2(delegated_claims, private_key)
    request_headers = {
        **caller_headers,
        "X-Internal-Token": service_token,
        "X-Trusted-Request-Context": signed_context,
    }
    request = urllib.request.Request(
        url,
        data=data,
        method=method.upper(),
        headers=request_headers,
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return response.read()


def _validated_source_context(context: ScopeView | TrustedRequestContext | dict, now: datetime) -> dict:
    """Normalize the source context to the fields needed for a V9.2
    TrustedRequestContext. Accepts a ScopeView (InvocationScope or
    LegacyScopeAdapter for the old AI Chat path) or a V9.2 TrustedRequestContext.
    The legacy single RequestContext protocol was removed in P3.9. Fails closed
    on anything else."""
    if context is None:
        raise TrustedContextError("invalid_context")

    if isinstance(context, TrustedRequestContext):
        data = context.model_dump()
        return {
            "run_id": data.get("run_id"),
            "principal_type": data.get("principal_type", "user"),
            "principal_id": data.get("principal_id"),
            "session_id": data.get("session_id"),
            "tenant_id": data["tenant_id"],
            "cluster_id": data.get("cluster_id"),
            "capability": data.get("capability"),
            "source": data.get("source", "planner"),
            "workload_kind": data.get("workload_kind", "platform"),
        }

    # ScopeView: InvocationScope (new ingress) or LegacyScopeAdapter (old AI Chat path).
    if isinstance(context, ScopeView):
        return {
            "run_id": str(getattr(context, "run_id", "") or ""),
            "principal_type": str(getattr(context, "principal_type", "user")),
            "principal_id": str(context.principal_id or ""),
            "session_id": str(context.session_id) if context.session_id else None,
            "tenant_id": str(context.tenant_id or ""),
            "cluster_id": str(context.cluster_id or ""),
            "capability": str(getattr(context, "capability", "") or ""),
            "source": str(getattr(context, "source", "planner")),
            "workload_kind": str(getattr(context, "workload_kind", "platform") or "platform"),
        }

    if isinstance(context, dict):
        if context.get("context_type") == "trusted_request":
            try:
                validated = TrustedRequestContext.model_validate(context)
            except Exception:
                raise TrustedContextError("invalid_context") from None
            _check_lifetime(validated.issued_at, validated.expires_at, now)
            return {
                "run_id": validated.run_id,
                "principal_type": validated.principal_type,
                "principal_id": validated.principal_id,
                "session_id": validated.session_id,
                "tenant_id": validated.tenant_id,
                "cluster_id": validated.cluster_id,
                "capability": validated.capability,
                "source": validated.source,
                "workload_kind": validated.workload_kind,
            }

    raise TrustedContextError("invalid_context")


def _check_lifetime(issued_at, expires_at, now: datetime) -> None:
    if expires_at.astimezone(timezone.utc) <= now:
        raise TrustedContextError("expired_context")
    if issued_at.astimezone(timezone.utc) > now:
        raise TrustedContextError("invalid_context")


def _load_private_key(encoded: str) -> Ed25519PrivateKey:
    if not encoded:
        raise TrustedContextError("invalid_signature")
    try:
        padded = encoded + "=" * (-len(encoded) % 4)
        raw = base64.b64decode(padded, altchars=b"-_", validate=True)
        return Ed25519PrivateKey.from_private_bytes(raw)
    except (binascii.Error, ValueError, TypeError):
        raise TrustedContextError("invalid_signature") from None


def _validate_query_api_url(url: str) -> None:
    configured = os.environ.get("QUERY_API_URL", "")
    if not configured or not isinstance(url, str):
        raise TrustedContextError("invalid_service")
    base = urllib.parse.urlsplit(configured.rstrip("/"))
    target = urllib.parse.urlsplit(url)
    if (
        base.scheme not in {"http", "https"}
        or target.scheme != base.scheme
        or target.hostname != base.hostname
        or target.port != base.port
        or target.username is not None
        or target.password is not None
        or bool(target.fragment)
    ):
        raise TrustedContextError("invalid_service")
    base_path = base.path.rstrip("/")
    # internal 路由（/internal/v1/query/*、/internal/v1/control-plane/*）注册在 query-api
    # 根路径（无 /api/v1 前缀），而 QUERY_API_URL 常含 /api/v1 用于公共 API。因此 internal
    # 路由的 target.path 应以根 /internal/v1/ 开头，而不是受 QUERY_API_URL 路径前缀约束。
    # host/port/scheme 校验（上面的 anti-SSRF）仍是安全关键，path 约束对 internal 放宽。
    is_internal = target.path.startswith("/internal/v1/")
    if is_internal:
        return
    if target.path != base_path and not target.path.startswith(base_path + "/"):
        raise TrustedContextError("invalid_service")
