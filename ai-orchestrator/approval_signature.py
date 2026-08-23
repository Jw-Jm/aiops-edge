"""EX.1 Ed25519 Approval Signature — V9.3 Execution Infrastructure。

将 Approval Signature 从"完整性校验"（contract_hash）升级为"授权签名"（Authorization Signature）：
- contract_hash（SHA256）：证明"内容没变"（Integrity）。
- ApprovalSignature（Ed25519）：证明"谁批准"（Authorization）。

核心约束（EX.1）：
- signer 必须 == contract.approved_by（谁批准谁签名，防 A 审批 B 冒用）。
- 验签失败 / 无 signature → 拒绝执行。
- key_id / public_key_version：支持密钥轮换（验签使用对应公钥）。
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Dict

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey


class SignatureInvalid(ValueError):
    def __init__(self, message: str):
        self.error_code = "SIGNATURE_INVALID"
        super().__init__(message)


@dataclass
class ApprovalSignature:
    signer: str
    algorithm: str
    payload: bytes
    signature: bytes
    signed_at: str
    key_id: str
    public_key_version: str


def _payload(contract_fields: Dict[str, Any]) -> bytes:
    """从 contract 关键字段计算签名 payload（防篡改）。"""
    raw = json.dumps(
        {
            "contract_id": contract_fields.get("contract_id"),
            "actions": sorted(contract_fields.get("actions", [])),
            "resources": sorted(contract_fields.get("resources", [])),
            "expire_time": str(contract_fields.get("expire_time")),
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return hashlib.sha256(raw).digest()


def sign_approval(
    *,
    contract_fields: Dict[str, Any],
    signer: str,
    private_key: Ed25519PrivateKey,
    key_id: str,
    public_key_version: str,
) -> ApprovalSignature:
    """审批人用私钥签名 contract 关键字段。"""
    payload = _payload(contract_fields)
    signature = private_key.sign(payload)
    return ApprovalSignature(
        signer=signer,
        algorithm="Ed25519",
        payload=payload,
        signature=signature,
        signed_at=datetime.now(timezone.utc).isoformat(),
        key_id=key_id,
        public_key_version=public_key_version,
    )


def verify_approval(
    sig: ApprovalSignature,
    *,
    contract_fields: Dict[str, Any],
    public_key: Ed25519PublicKey,
    expected_signer: str,
) -> bool:
    """验签：signer 必须 == expected_signer；payload 必须未被篡改；签名必须有效。"""
    # 谁批准谁签名
    if sig.signer != expected_signer:
        raise SignatureInvalid(f"signer 与 approved_by 不一致: {sig.signer} != {expected_signer}")
    # payload 防篡改（当前字段重算 == 签名时 payload）
    current_payload = _payload(contract_fields)
    if current_payload != sig.payload:
        raise SignatureInvalid("contract 关键字段被篡改（payload 不匹配）")
    # 签名有效（用对应公钥验签）
    try:
        public_key.verify(sig.signature, sig.payload)
    except Exception:
        raise SignatureInvalid("签名无效（公钥验签失败）") from None
    return True
