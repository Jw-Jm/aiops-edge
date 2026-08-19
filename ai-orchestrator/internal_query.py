"""Fail-closed orchestrator calls to the query-api trust boundary."""

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

from contracts import RequestContext
from trusted_context import TrustedContextError, sign_trusted_request_context


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
    context: RequestContext,
    method: str = "GET",
    data: bytes | None = None,
    headers: Mapping[str, str] | None = None,
    timeout: int | float = _DEFAULT_TIMEOUT,
) -> bytes:
    """Call query-api with separate service and signed-context credentials.

    ``context`` must be the explicit, already-authorized context received by the
    orchestrator. The helper preserves its authorization scope while minting a
    fresh audience-bound request ID, nonce, and short validity window.
    """

    now = datetime.now(timezone.utc)
    source_context = _validated_source_context(context, now)
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

    delegated_context = source_context.model_copy(
        update={
            "issuer": _ISSUER,
            "audience": _AUDIENCE,
            "request_id": uuid4(),
            "issued_at": now,
            "expires_at": now + _CONTEXT_LIFETIME,
            "nonce": uuid4(),
        }
    )
    signed_context = sign_trusted_request_context(
        delegated_context.model_dump(), private_key
    )
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


def _validated_source_context(
    context: RequestContext | None, now: datetime
) -> RequestContext:
    if not isinstance(context, RequestContext):
        raise TrustedContextError("invalid_context")
    try:
        validated = RequestContext.model_validate(context.model_dump())
    except Exception:
        raise TrustedContextError("invalid_context") from None
    if validated.expires_at.astimezone(timezone.utc) <= now:
        raise TrustedContextError("expired_context")
    if validated.issued_at.astimezone(timezone.utc) > now:
        raise TrustedContextError("invalid_context")
    return validated


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
    if target.path != base_path and not target.path.startswith(base_path + "/"):
        raise TrustedContextError("invalid_service")
