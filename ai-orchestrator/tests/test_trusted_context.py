import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone
from uuid import UUID

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from trusted_context import TrustedContextError, sign_trusted_request_context


def test_sign_trusted_request_context_produces_eddsa_jws_without_authorization_fields():
    private_key = Ed25519PrivateKey.generate()
    token = sign_trusted_request_context(context(), private_key)

    header, payload, signature = token.split(".")
    protected_header = json.loads(decode(header))
    assert protected_header["alg"] == "EdDSA"
    assert protected_header["typ"] == "JWT"
    assert protected_header["kid"] == key_id(private_key.public_key().public_bytes_raw())
    claims = json.loads(decode(payload))
    assert claims["cluster_id"] == "66666666-6666-4666-8666-666666666666"
    assert {"roles", "permissions", "allowed_clusters", "credential_ref", "approval"}.isdisjoint(claims)
    assert len(base64.urlsafe_b64decode(signature + "==")) == 64


@pytest.mark.parametrize("field", ["roles", "permissions", "allowed_clusters", "credential_ref", "approval"])
def test_sign_trusted_request_context_excludes_untrusted_authorization_fields(field):
    supplied = context()
    supplied[field] = ["caller-controlled"]

    token = sign_trusted_request_context(supplied, Ed25519PrivateKey.generate())

    assert field not in json.loads(decode(token.split(".")[1]))


@pytest.mark.parametrize(
    "mutate, expected_code",
    [
        (lambda value: value.update(expires_at=value["issued_at"] + timedelta(seconds=29)), "invalid_context"),
        (lambda value: value.update(expires_at=value["issued_at"] + timedelta(seconds=61)), "invalid_context"),
        (lambda value: value.update(expires_at=value["issued_at"] - timedelta(seconds=1)), "expired_context"),
    ],
)
def test_sign_trusted_request_context_rejects_invalid_expiry(mutate, expected_code):
    supplied = context()
    mutate(supplied)

    with pytest.raises(TrustedContextError) as error:
        sign_trusted_request_context(supplied, Ed25519PrivateKey.generate())

    assert error.value.error_code == expected_code


def test_signature_detects_payload_tampering():
    private_key = Ed25519PrivateKey.generate()
    token = sign_trusted_request_context(context(), private_key)
    header, payload, signature = token.split(".")
    claims = json.loads(decode(payload))
    claims["cluster_id"] = "88888888-8888-4888-8888-888888888888"
    tampered = f"{header}.{encode(claims)}.{signature}"

    with pytest.raises(Exception):
        private_key.public_key().verify(base64.urlsafe_b64decode(signature + "=="), tampered.rsplit(".", 1)[0].encode())


def context():
    issued_at = datetime.now(timezone.utc).replace(microsecond=0)
    return {
        "version": 1,
        "issuer": "ai-orchestrator",
        "audience": "ai-apm-query-go",
        "request_id": UUID("11111111-1111-4111-8111-111111111111"),
        "run_id": UUID("22222222-2222-4222-8222-222222222222"),
        "user_id": UUID("33333333-3333-4333-8333-333333333333"),
        "session_id": UUID("44444444-4444-4444-8444-444444444444"),
        "tenant_id": UUID("55555555-5555-4555-8555-555555555555"),
        "cluster_id": UUID("66666666-6666-4666-8666-666666666666"),
        "source": "planner",
        "capability": "kubernetes.read",
        "issued_at": issued_at,
        "expires_at": issued_at + timedelta(seconds=30),
        "nonce": UUID("77777777-7777-4777-8777-777777777777"),
    }


def decode(value):
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))


def encode(value):
    serialized = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return base64.urlsafe_b64encode(serialized).rstrip(b"=").decode()


def key_id(public_key):
    return base64.urlsafe_b64encode(hashlib.sha256(public_key).digest()).rstrip(b"=").decode()
