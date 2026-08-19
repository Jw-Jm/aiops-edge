"""Signing-only TrustedRequestContext primitives for the orchestrator."""

import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone
from typing import Any, Mapping
from uuid import UUID

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey


class TrustedContextError(ValueError):
    """Stable error used by adapters without exposing signing details."""

    def __init__(self, error_code: str):
        self.error_code = error_code
        super().__init__(error_code)


_EXCLUDED_FIELDS = frozenset({"roles", "permissions", "allowed_clusters", "credential_ref", "approval"})
_REQUIRED_FIELDS = frozenset({"version", "issuer", "audience", "request_id", "run_id", "user_id", "session_id", "tenant_id", "cluster_id", "source", "capability", "issued_at", "expires_at", "nonce"})


def sign_trusted_request_context(context: Mapping[str, Any], private_key: Ed25519PrivateKey) -> str:
    """Return an EdDSA JWS for a canonical, short-lived request context."""

    claims = {key: value for key, value in context.items() if key not in _EXCLUDED_FIELDS}

    if set(claims) != _REQUIRED_FIELDS:
        raise TrustedContextError("invalid_context")
    _validate_claims(claims)

    public_key = private_key.public_key().public_bytes_raw()
    header = {"alg": "EdDSA", "kid": _key_id(public_key), "typ": "JWT"}
    signing_input = f"{_encode(header)}.{_encode(claims)}".encode("ascii")
    signature = private_key.sign(signing_input)
    return f"{signing_input.decode('ascii')}.{_b64url(signature)}"


def _validate_claims(claims: Mapping[str, Any]) -> None:
    if not isinstance(claims["version"], int) or claims["version"] < 1:
        raise TrustedContextError("invalid_context")
    if any(not isinstance(claims[field], str) or not claims[field] for field in ("issuer", "audience", "source", "capability")):
        raise TrustedContextError("invalid_context")
    try:
        for field in ("request_id", "run_id", "user_id", "session_id", "tenant_id", "cluster_id", "nonce"):
            value = UUID(str(claims[field]))
            if str(value) != str(claims[field]):
                raise ValueError(field)
    except (ValueError, TypeError, AttributeError):
        raise TrustedContextError("invalid_context") from None
    issued_at = claims["issued_at"]
    expires_at = claims["expires_at"]
    if not isinstance(issued_at, datetime) or not isinstance(expires_at, datetime) or issued_at.tzinfo is None or expires_at.tzinfo is None:
        raise TrustedContextError("invalid_context")
    lifetime = expires_at.astimezone(timezone.utc) - issued_at.astimezone(timezone.utc)
    if lifetime <= timedelta(0):
        raise TrustedContextError("expired_context")
    if lifetime < timedelta(seconds=30) or lifetime > timedelta(seconds=60):
        raise TrustedContextError("invalid_context")


def _encode(value: Mapping[str, Any]) -> str:
    return _b64url(json.dumps(value, default=_json_default, sort_keys=True, separators=(",", ":")).encode("utf-8"))


def _json_default(value: Any) -> str:
    if isinstance(value, datetime):
        return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    if isinstance(value, UUID):
        return str(value)
    raise TypeError(f"unsupported TrustedRequestContext value: {type(value)!r}")


def _key_id(public_key: bytes) -> str:
    return _b64url(hashlib.sha256(public_key).digest())


def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")
