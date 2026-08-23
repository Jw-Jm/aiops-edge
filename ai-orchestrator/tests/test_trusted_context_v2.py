import base64
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey

from trusted_context import (
    ReplayCache,
    TrustedContextError,
    VerifyConfig,
    sign_trusted_request_context_v2,
    verify_run_control_context,
    verify_run_invocation_context,
    verify_trusted_request_context_v2,
)


def _now():
    return datetime.now(timezone.utc)


def _keypair():
    private_key = Ed25519PrivateKey.generate()
    return private_key


def _public_key(private_key) -> Ed25519PublicKey:
    return private_key.public_key()


def _kid(private_key) -> str:
    raw = private_key.public_key().public_bytes_raw()
    return base64.urlsafe_b64encode(__import__("hashlib").sha256(raw).digest()).rstrip(b"=").decode("ascii")


def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def _make_run_invocation(private_key, now=None, **overrides):
    now = now or _now()
    claims = {
        "version": 1,
        "context_type": "run_invocation",
        "issuer": "query-api",
        "audience": "ai-orchestrator",
        "request_id": "11111111-1111-4111-8111-111111111111",
        "principal_type": "user",
        "principal_id": "33333333-3333-4333-8333-333333333333",
        "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": "55555555-5555-4555-8555-555555555555",
        "source": "frontend",
        "cluster_scope": ["66666666-6666-4666-8666-666666666666"],
        "issued_at": now,
        "expires_at": now + timedelta(seconds=30),
        "nonce": "77777777-7777-4777-8777-777777777777",
    }
    claims.update(overrides)
    public_key = _public_key(private_key)
    header = {"alg": "EdDSA", "kid": _kid(private_key), "typ": "AIOPS-CONTEXT"}
    signing_input = f"{_b64url(json.dumps(header, sort_keys=True, separators=(',', ':')).encode())}.{_b64url(json.dumps(claims, default=str, sort_keys=True, separators=(',', ':')).encode())}"
    signature = private_key.sign(signing_input.encode("ascii"))
    return f"{signing_input}.{_b64url(signature)}"


def _inv_cfg(private_key) -> VerifyConfig:
    return VerifyConfig(
        issuer="query-api",
        audience="ai-orchestrator",
        public_keys={_kid(private_key): _public_key(private_key)},
        replay_cache=ReplayCache(max_items=100),
        clock_skew_seconds=30,
    )


def test_python_verify_run_invocation_roundtrip():
    private_key = _keypair()
    token = _make_run_invocation(private_key)
    claims = verify_run_invocation_context(token, _inv_cfg(private_key), _now())
    assert claims["context_type"] == "run_invocation"
    assert claims["tenant_id"] == "55555555-5555-4555-8555-555555555555"


def test_python_rejects_wrong_type_on_endpoint():
    private_key = _keypair()
    # Build a run_control token and send it to the run_invocation verifier.
    now = _now()
    claims = {
        "version": 1, "context_type": "run_control", "issuer": "query-api", "audience": "ai-orchestrator",
        "request_id": "11111111-1111-4111-8111-111111111111", "run_id": "22222222-2222-4222-8222-222222222222",
        "operation": "cancel", "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
        "session_id": "44444444-4444-4444-8444-444444444444", "tenant_id": "55555555-5555-4555-8555-555555555555",
        "issued_at": now, "expires_at": now + timedelta(seconds=30), "nonce": "77777777-7777-4777-8777-777777777777",
    }
    header = {"alg": "EdDSA", "kid": _kid(private_key), "typ": "AIOPS-CONTEXT"}
    si = f"{_b64url(json.dumps(header, sort_keys=True, separators=(',', ':')).encode())}.{_b64url(json.dumps(claims, default=str, sort_keys=True, separators=(',', ':')).encode())}"
    sig = private_key.sign(si.encode("ascii"))
    ctrl_token = f"{si}.{_b64url(sig)}"
    with pytest.raises(TrustedContextError) as exc:
        verify_run_invocation_context(ctrl_token, _inv_cfg(private_key), _now())
    assert exc.value.error_code == "invalid_context"


def test_python_rejects_tampered_and_expired():
    private_key = _keypair()
    cfg = _inv_cfg(private_key)
    token = _make_run_invocation(private_key)

    # tamper payload
    parts = token.split(".")
    tampered = parts[0] + "." + parts[1][:-1] + "A" + "." + parts[2]
    with pytest.raises(TrustedContextError):
        verify_run_invocation_context(tampered, cfg, _now())

    # expired
    now = _now()
    expired = _make_run_invocation(private_key, now=now, issued_at=now - timedelta(minutes=5), expires_at=now - timedelta(minutes=4))
    with pytest.raises(TrustedContextError) as exc:
        verify_run_invocation_context(expired, cfg, _now())
    assert exc.value.error_code == "expired_context"


def test_python_rejects_replay():
    private_key = _keypair()
    cfg = _inv_cfg(private_key)
    token = _make_run_invocation(private_key)
    verify_run_invocation_context(token, cfg, _now())
    with pytest.raises(TrustedContextError) as exc:
        verify_run_invocation_context(token, cfg, _now())
    assert exc.value.error_code == "context_replayed"


def test_python_rejects_typ_jwt():
    # A typ=JWT token (legacy) must be rejected by the V2 verifier. The legacy
    # production signer was removed in P3.9; construct the token inline.
    private_key = _keypair()
    legacy = {
        "version": 1, "issuer": "query-api", "audience": "ai-orchestrator",
        "request_id": "11111111-1111-4111-8111-111111111111", "run_id": "22222222-2222-4222-8222-222222222222",
        "user_id": "33333333-3333-4333-8333-333333333333", "session_id": "44444444-4444-4444-8444-444444444444",
        "tenant_id": "55555555-5555-4555-8555-555555555555", "cluster_id": "66666666-6666-4666-8666-666666666666",
        "source": "planner", "capability": "observability.logs.read",
        "issued_at": _now(), "expires_at": _now() + timedelta(seconds=30), "nonce": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    }
    header = {"alg": "EdDSA", "kid": _kid(private_key), "typ": "JWT"}
    enc = lambda o: _b64url(json.dumps(o, default=str, sort_keys=True, separators=(",", ":")).encode())
    signing_input = f"{enc(header)}.{enc(legacy)}"
    sig = private_key.sign(signing_input.encode("ascii"))
    legacy_token = f"{signing_input}.{_b64url(sig)}"
    cfg = VerifyConfig(
        issuer="query-api", audience="ai-orchestrator",
        public_keys={_kid(private_key): _public_key(private_key)},
        replay_cache=ReplayCache(max_items=100), clock_skew_seconds=30,
    )
    with pytest.raises(TrustedContextError):
        verify_run_invocation_context(legacy_token, cfg, _now())
