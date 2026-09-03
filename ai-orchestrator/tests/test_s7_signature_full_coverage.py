"""S7 回归：审批签名与 contract_hash 必须覆盖全部授权字段。

历史漏洞：签名 payload / contract_hash 只白名单 4 字段（contract_id/actions/
resources/expire_time），篡改 allowed_tools（扩大工具面）、max_scope（namespace→
cluster 提权）、rollback_policy（关回滚保护）不改变签名/hash，验签仍通过。
"""
from __future__ import annotations

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from approval_signature import SignatureInvalid, sign_approval, verify_approval
from execution_contract import ExecutionContractStore


def _full_fields(contract):
    """与 execution_adapter._verify_signature 保持一致的字段集。"""
    return {
        "contract_id": contract.contract_id,
        "actions": contract.allowed_actions,
        "resources": contract.allowed_resources,
        "tools": contract.allowed_tools,
        "max_scope": contract.max_scope,
        "rollback_policy": contract.rollback_policy,
        "expire_time": str(contract.expire_time),
    }


def _make_contract():
    store = ExecutionContractStore()
    c = store.create(
        plan_id="p", intent_id="i", run_id="r", requested_by="agent-1",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"],
        allowed_actions=["restart"], max_scope="namespace",
        expire_time=__import__("datetime").datetime.now(__import__("datetime").timezone.utc)
        + __import__("datetime").timedelta(minutes=5),
        rollback_policy={"auto_rollback": True},
    )
    c = store.approve(c.contract_id, approved_by="human-1")
    return store, store.activate(c.contract_id)


def _sign(fields):
    key = Ed25519PrivateKey.generate()
    sig = sign_approval(contract_fields=fields, signer="human-1",
                        private_key=key, key_id="k", public_key_version="v1")
    return sig, key.public_key()


def test_signature_rejects_privilege_escalation_tampering():
    _, contract = _make_contract()
    sig, pub = _sign(_full_fields(contract))
    for field, escalated in [
        ("tools", ["execute_k8s.v1", "shell_exec.v1"]),
        ("max_scope", "cluster"),
        ("rollback_policy", {"auto_rollback": False}),
    ]:
        tampered = _full_fields(contract)
        tampered[field] = escalated
        with pytest.raises(SignatureInvalid):
            verify_approval(sig, contract_fields=tampered, public_key=pub, expected_signer="human-1")


def test_contract_hash_detects_authz_field_tampering():
    store, contract = _make_contract()
    assert store.verify_hash(contract.contract_id) is True
    for field, escalated in [
        ("allowed_tools", ["execute_k8s.v1", "shell_exec.v1"]),
        ("max_scope", "cluster"),
        ("rollback_policy", {"auto_rollback": False}),
    ]:
        store._store[contract.contract_id] = __import__("dataclasses").replace(
            contract, **{field: escalated})
        assert store.verify_hash(contract.contract_id) is False, f"hash must detect {field} tampering"
