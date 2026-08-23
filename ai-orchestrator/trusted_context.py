"""V9.2 TrustedContext primitives for the orchestrator.

Signing side (orchestrator → query-api): TrustedRequestContext.
Verifying side (query-api → orchestrator): RunInvocationContext / RunControlContext.

JWS EdDSA / Ed25519, typ=AIOPS-CONTEXT. Legacy single RequestContext signing is
retained as PHASE3_TRANSITION_ONLY until P3.9 cutover.
"""

import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone
from typing import Any, Mapping, Optional
from uuid import UUID

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey

_JWS_TYPE = "AIOPS-CONTEXT"
_LEGACY_JWS_TYPE = "JWT"


class TrustedContextError(ValueError):
    """Stable error used by adapters without exposing signing details."""

    def __init__(self, error_code: str):
        self.error_code = error_code
        super().__init__(error_code)


# ── shared helpers ─────────────────────────────────────────────────────

def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def _json_default(value: Any) -> str:
    if isinstance(value, datetime):
        return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    if isinstance(value, UUID):
        return str(value)
    raise TypeError(f"unsupported context value: {type(value)!r}")


def _encode(value: Mapping[str, Any]) -> str:
    return _b64url(json.dumps(value, default=_json_default, sort_keys=True, separators=(",", ":")).encode("utf-8"))


def _key_id(public_key: bytes) -> str:
    return _b64url(hashlib.sha256(public_key).digest())


def _check_lifetime(issued_at, expires_at) -> None:
    if not isinstance(issued_at, datetime) or not isinstance(expires_at, datetime) or issued_at.tzinfo is None or expires_at.tzinfo is None:
        raise TrustedContextError("invalid_context")
    lifetime = expires_at.astimezone(timezone.utc) - issued_at.astimezone(timezone.utc)
    if lifetime <= timedelta(0):
        raise TrustedContextError("expired_context")
    if lifetime < timedelta(seconds=30) or lifetime > timedelta(seconds=60):
        raise TrustedContextError("invalid_context")


def _check_uuid_fields(claims: Mapping[str, Any], fields) -> None:
    try:
        for field in fields:
            value = claims.get(field)
            if value is None or value == "":
                continue  # nullable optional fields
            parsed = UUID(str(value))
            if str(parsed) != str(value):
                raise ValueError(field)
    except (ValueError, TypeError, AttributeError):
        raise TrustedContextError("invalid_context") from None


# ═══════════════════════════════════════════════════════════════════════
# V9.2 signing (orchestrator → query-api): TrustedRequestContext
# ═══════════════════════════════════════════════════════════════════════

def sign_trusted_request_context_v2(context: Mapping[str, Any], private_key: Ed25519PrivateKey) -> str:
    """Return an EdDSA JWS (typ=AIOPS-CONTEXT) for a V9.2 TrustedRequestContext."""
    return _sign_v2(context, private_key, expected_type="trusted_request")


def _sign_v2(context: Mapping[str, Any], private_key: Ed25519PrivateKey, expected_type: str) -> str:
    if context.get("context_type") != expected_type:
        raise TrustedContextError("invalid_context")
    public_key = private_key.public_key().public_bytes_raw()
    header = {"alg": "EdDSA", "kid": _key_id(public_key), "typ": _JWS_TYPE}
    signing_input = f"{_encode(header)}.{_encode(context)}".encode("ascii")
    signature = private_key.sign(signing_input)
    return f"{signing_input.decode('ascii')}.{_b64url(signature)}"


# ═══════════════════════════════════════════════════════════════════════
# V9.2 verifying (query-api → orchestrator)
# ═══════════════════════════════════════════════════════════════════════

class VerifyConfig:
    def __init__(self, *, issuer: str, audience: str, public_keys: Mapping[str, Ed25519PublicKey],
                 replay_cache: Optional["ReplayCache"] = None, clock_skew_seconds: int = 30):
        self.issuer = issuer
        self.audience = audience
        self.public_keys = dict(public_keys)
        self.replay_cache = replay_cache
        self.clock_skew = timedelta(seconds=clock_skew_seconds)


class ReplayCache:
    """Bounded in-memory nonce replay cache."""

    def __init__(self, max_items: int = 4096):
        self._nonces: set[str] = set()
        self._max = max_items

    def check_and_store(self, nonce: str) -> bool:
        if nonce in self._nonces:
            return False
        if len(self._nonces) >= self._max:
            return False
        self._nonces.add(nonce)
        return True


def verify_run_invocation_context(token: str, cfg: VerifyConfig, now: datetime):
    """Verify and return a RunInvocationContext payload dict (or raise)."""
    payload = _verify_v2(token, cfg, expected_type="run_invocation", now=now)
    return payload


def verify_run_control_context(token: str, cfg: VerifyConfig, now: datetime):
    """Verify and return a RunControlContext payload dict (or raise)."""
    payload = _verify_v2(token, cfg, expected_type="run_control", now=now)
    return payload


def verify_trusted_request_context_v2(token: str, cfg: VerifyConfig, now: datetime):
    """Verify and return a TrustedRequestContext payload dict (or raise)."""
    payload = _verify_v2(token, cfg, expected_type="trusted_request", now=now)
    return payload


def _verify_v2(token: str, cfg: VerifyConfig, expected_type: str, now: datetime) -> dict:
    if cfg.replay_cache is None or not cfg.public_keys:
        raise TrustedContextError("invalid_context")
    parts = token.split(".")
    if len(parts) != 3 or not all(parts):
        raise TrustedContextError("invalid_signature")
    try:
        header = json.loads(base64.urlsafe_b64decode(parts[0] + "=" * (-len(parts[0]) % 4)))
        payload_bytes = base64.urlsafe_b64decode(parts[1] + "=" * (-len(parts[1]) % 4))
        signature = base64.urlsafe_b64decode(parts[2] + "=" * (-len(parts[2]) % 4))
    except Exception:
        raise TrustedContextError("invalid_signature") from None

    if header.get("alg") != "EdDSA" or header.get("typ") != _JWS_TYPE or not header.get("kid"):
        raise TrustedContextError("invalid_signature")
    public_key = cfg.public_keys.get(header.get("kid"))
    if public_key is None:
        raise TrustedContextError("invalid_signature")

    signing_input = f"{parts[0]}.{parts[1]}".encode("ascii")
    try:
        public_key.verify(signature, signing_input)
    except Exception:
        raise TrustedContextError("invalid_signature") from None

    try:
        claims = json.loads(payload_bytes)
    except Exception:
        raise TrustedContextError("invalid_context") from None

    # context_type must match the expected endpoint type (confused-deputy prevention).
    if claims.get("context_type") != expected_type:
        raise TrustedContextError("invalid_context")

    if claims.get("issuer") != cfg.issuer or claims.get("audience") != cfg.audience:
        raise TrustedContextError("invalid_context")

    issued_at = _parse_datetime(claims.get("issued_at"))
    expires_at = _parse_datetime(claims.get("expires_at"))
    if issued_at is None or expires_at is None:
        raise TrustedContextError("invalid_context")
    if expires_at < now - cfg.clock_skew:
        raise TrustedContextError("expired_context")
    if issued_at > now + cfg.clock_skew:
        raise TrustedContextError("invalid_context")

    nonce = str(claims.get("nonce") or "")
    if not cfg.replay_cache.check_and_store(nonce):
        raise TrustedContextError("context_replayed")

    return claims


def _parse_datetime(value) -> Optional[datetime]:
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return None
    return parsed.astimezone(timezone.utc)



