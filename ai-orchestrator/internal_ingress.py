"""V9.2 trusted internal ingress for the orchestrator (P3.9-B1).

The new RunInvocation ingress is the only privileged entry for browser→query-api→
orchestrator investigation requests. It verifies:
  - service credential (X-Internal-Token)
  - EdDSA JWS, typ=AIOPS-CONTEXT, kid, issuer, audience, iat/exp, nonce/replay
  - context_type=run_invocation
  - tenant/body and cluster/body scope consistency

Then it builds an internal InvocationScope (not a legacy RequestContext) and hands
it to the existing AI Chat business implementation.
"""

from __future__ import annotations

import base64
import binascii
import os
from dataclasses import dataclass

from fastapi import HTTPException, Request
from pydantic import ValidationError

from invocation_scope import InvocationScope, ValidationFailed
from trusted_context import ReplayCache, TrustedContextError, VerifyConfig, verify_run_control_context, verify_run_invocation_context

_INVOCATION_ISSUER = "query-api"
_INVOCATION_AUDIENCE = "ai-orchestrator"
_CLOCK_SKEW_SECONDS = 30

# Shared bounded replay cache across ingress calls so nonce replay is detected.
_REPLAY_CACHE = ReplayCache(max_items=4096)


def _load_public_keys() -> dict[str, object]:
    """Load the query-api public verification keys (kid → Ed25519 public key)."""
    encoded = os.environ.get("QUERY_TO_ORCHESTRATOR_VERIFY_KEYS", "").strip()
    if not encoded:
        return {}
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

    keys: dict[str, object] = {}
    for item in encoded.split(","):
        item = item.strip()
        if not item:
            continue
        try:
            raw = base64.b64decode(item + "=" * (-len(item) % 4), altchars=b"-_", validate=True)
            pub = Ed25519PublicKey.from_public_bytes(raw)
        except (binascii.Error, ValueError, TypeError):
            continue
        kid = _kid_for(pub)
        keys[kid] = pub
    return keys


def _kid_for(public_key) -> str:
    import hashlib

    digest = hashlib.sha256(public_key.public_bytes_raw()).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode()


def _expected_service_token() -> str:
    return os.environ.get("QUERY_TO_ORCHESTRATOR_TOKEN") or os.environ.get("INTERNAL_TOKEN", "")


def _normalize_run_invocation_claims(claims: dict) -> dict:
    """Normalize the cross-language empty system-session sentinel.

    The Go internal contract represents a system principal's absent session as
    ``session_id: ""`` because its wire struct uses strings.  Pydantic's
    ``Optional[UUID]`` correctly rejects that empty string, so normalize only
    this exact, already-signed system-principal case before model validation.
    User sessions and every other malformed value remain fail-closed.
    """
    normalized = dict(claims)
    if normalized.get("principal_type") == "system" and normalized.get("session_id") == "":
        normalized["session_id"] = None
    return normalized


def verify_run_invocation_ingress(request: Request) -> dict:
    """Verify service credential + RunInvocationContext and return the claims.

    Returns the verified RunInvocationContext payload. Raises HTTPException(401)
    on any verification failure, or HTTPException(409) on nonce replay.
    """
    token = request.headers.get("X-Internal-Token", "")
    provided = os.environ.get("X-INTERNAL-TOKEN", "")
    _ = provided
    service_token = _expected_service_token()
    if not service_token:
        raise HTTPException(status_code=401, detail="invalid_service")
    if not _constant_time_eq(token, service_token):
        raise HTTPException(status_code=401, detail="invalid_service")

    public_keys = _load_public_keys()
    if not public_keys:
        raise HTTPException(status_code=401, detail="invalid_context")

    ctx_header = request.headers.get("X-Trusted-Request-Context", "")
    cfg = VerifyConfig(
        issuer=_INVOCATION_ISSUER,
        audience=_INVOCATION_AUDIENCE,
        public_keys=public_keys,
        replay_cache=_REPLAY_CACHE,
        clock_skew_seconds=_CLOCK_SKEW_SECONDS,
    )
    try:
        claims = _normalize_run_invocation_claims(
            verify_run_invocation_context(ctx_header, cfg, _now_utc())
        )
        # Signature verification alone checks transport claims; Pydantic enforces
        # the capability-specific Run identity invariant on the wire.
        from contracts import RunInvocationContext
        RunInvocationContext.model_validate(claims)
        return claims
    except ValidationError:
        # A valid signature does not make malformed scope/principal claims
        # trustworthy. Never return unvalidated claims to the business layer;
        # endpoint-specific authorization is applied only after this boundary.
        if request.url.path.endswith("/chat") and claims.get("capability") != "ai.chat":
            raise HTTPException(status_code=403, detail="CAPABILITY_DENIED") from None
        if request.url.path.endswith("/run-invocations") and claims.get("capability") != "ai.investigate":
            raise HTTPException(status_code=403, detail="CAPABILITY_DENIED") from None
        if claims.get("principal_type") == "system":
            raise HTTPException(status_code=403, detail="SYSTEM_PRINCIPAL_DENIED") from None
        raise HTTPException(status_code=403, detail="INVALID_CONTEXT") from None
    except TrustedContextError as exc:
        code = getattr(exc, "error_code", "invalid_context")
        if code in ("context_replayed", "CONTEXT_REPLAYED"):
            raise HTTPException(status_code=409, detail="CONTEXT_REPLAYED") from None
        raise HTTPException(status_code=401, detail="invalid_context") from None


def verify_run_control_ingress(request: Request, expected_operation: str) -> dict:
    """Verify service credential + RunControlContext and enforce the endpoint's
    operation binding (confused-deputy prevention). A valid context of the wrong
    operation is rejected, as are non-run_control contexts.
    """
    token = request.headers.get("X-Internal-Token", "")
    service_token = _expected_service_token()
    if not service_token:
        raise HTTPException(status_code=401, detail="invalid_service")
    if not _constant_time_eq(token, service_token):
        raise HTTPException(status_code=401, detail="invalid_service")

    public_keys = _load_public_keys()
    if not public_keys:
        raise HTTPException(status_code=401, detail="invalid_context")

    ctx_header = request.headers.get("X-Trusted-Request-Context", "")
    cfg = VerifyConfig(
        issuer=_INVOCATION_ISSUER,
        audience=_INVOCATION_AUDIENCE,
        public_keys=public_keys,
        replay_cache=_REPLAY_CACHE,
        clock_skew_seconds=_CLOCK_SKEW_SECONDS,
    )
    try:
        claims = verify_run_control_context(ctx_header, cfg, _now_utc())
    except TrustedContextError as exc:
        code = getattr(exc, "error_code", "invalid_context")
        if code in ("context_replayed", "CONTEXT_REPLAYED"):
            raise HTTPException(status_code=409, detail="CONTEXT_REPLAYED") from None
        raise HTTPException(status_code=401, detail="invalid_context") from None
    if claims.get("operation") != expected_operation:
        raise HTTPException(status_code=403, detail="ACTION_NOT_ALLOWED")
    return claims


def build_invocation_scope(claims: dict) -> InvocationScope:
    try:
        return InvocationScope.from_run_invocation_context(claims)
    except ValidationFailed as exc:
        raise HTTPException(status_code=422, detail=exc.error_code) from None
    except TrustedContextError as exc:
        raise HTTPException(status_code=401, detail=getattr(exc, "error_code", "invalid_context")) from None


def _constant_time_eq(left: str, right: str) -> bool:
    import hmac

    return hmac.compare_digest(left.encode(), right.encode())


def _now_utc():
    from datetime import datetime, timezone

    return datetime.now(timezone.utc)
