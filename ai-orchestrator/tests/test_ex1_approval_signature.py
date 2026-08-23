"""EX.1 Ed25519 Approval Signature — TDD 测试（V9.3 Execution Infrastructure）。

覆盖 EX.1 设计 + 评审补充（key_id/public_key_version）：
- T1 有效 signer 签名 → 验签通过
- T2 signer ≠ expected_signer（approved_by）→ 拒绝（防 A 审批 B 冒用）
- T3 篡改 payload → 验签失败
- T4 无 signature → 拒绝执行（签名对象必有 signature）
- T5 key_id / public_key_version 存在（密钥轮换，验签用哪把公钥）
"""
from __future__ import annotations

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from approval_signature import (
    ApprovalSignature,
    SignatureInvalid,
    sign_approval,
    verify_approval,
)


def _contract_fields():
    return {
        "contract_id": "c-1",
        "actions": ["restart"],
        "resources": ["ns-a"],
        "expire_time": "2026-08-20T00:05:00Z",
    }


@pytest.fixture
def signer_key():
    return Ed25519PrivateKey.generate()


@pytest.fixture
def signature(signer_key):
    return sign_approval(
        contract_fields=_contract_fields(),
        signer="user-1",
        private_key=signer_key,
        key_id="key-1",
        public_key_version="v1",
    )


# ═══════════════════════════════════════════════════════
#  T1 有效签名验签通过
# ═══════════════════════════════════════════════════════

class TestT1ValidSignature:
    def test_verify_passes(self, signature, signer_key):
        ok = verify_approval(
            signature,
            contract_fields=_contract_fields(),
            public_key=signer_key.public_key(),
            expected_signer="user-1",
        )
        assert ok is True


# ═══════════════════════════════════════════════════════
#  T2 signer ≠ expected_signer（谁批准谁签名）
# ═══════════════════════════════════════════════════════

class TestT2SignerMismatch:
    def test_wrong_signer_rejected(self, signature, signer_key):
        with pytest.raises(SignatureInvalid):
            verify_approval(
                signature,
                contract_fields=_contract_fields(),
                public_key=signer_key.public_key(),
                expected_signer="user-2",  # A 审批，B 试图冒用
            )


# ═══════════════════════════════════════════════════════
#  T3 篡改 payload 验签失败
# ═══════════════════════════════════════════════════════

class TestT3Tamper:
    def test_tampered_fields_rejected(self, signature, signer_key):
        tampered = _contract_fields()
        tampered["actions"] = ["delete"]  # 运行期篡改
        with pytest.raises(SignatureInvalid):
            verify_approval(
                signature,
                contract_fields=tampered,
                public_key=signer_key.public_key(),
                expected_signer="user-1",
            )

    def test_tampered_signature_rejected(self, signature, signer_key):
        bad = ApprovalSignature(
            signer=signature.signer,
            algorithm=signature.algorithm,
            payload=signature.payload,
            signature="tampered-bytes",
            signed_at=signature.signed_at,
            key_id=signature.key_id,
            public_key_version=signature.public_key_version,
        )
        with pytest.raises(SignatureInvalid):
            verify_approval(
                bad,
                contract_fields=_contract_fields(),
                public_key=signer_key.public_key(),
                expected_signer="user-1",
            )


# ═══════════════════════════════════════════════════════
#  T4 必有 signature
# ═══════════════════════════════════════════════════════

class TestT4SignaturePresent:
    def test_signature_object_has_sig(self, signature):
        assert signature.signature  # 签名必有内容
        assert signature.algorithm == "Ed25519"
        assert signature.signer == "user-1"


# ═══════════════════════════════════════════════════════
#  T5 key_id / public_key_version（密钥轮换）
# ═══════════════════════════════════════════════════════

class TestT5KeyId:
    def test_key_id_present(self, signature):
        assert signature.key_id == "key-1"
        assert signature.public_key_version == "v1"

    def test_wrong_key_id_rejected(self, signature, signer_key):
        # 验签用错公钥（不同 key_id）→ 拒绝
        other_key = Ed25519PrivateKey.generate()
        with pytest.raises(SignatureInvalid):
            verify_approval(
                signature,
                contract_fields=_contract_fields(),
                public_key=other_key.public_key(),
                expected_signer="user-1",
            )
