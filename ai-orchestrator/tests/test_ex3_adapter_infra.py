"""EX.3 Adapter 增强 — TDD 测试（V9.3 Execution Infrastructure）。

覆盖 EX.3 + R4.1/R4.2：
- R4.1 Execution Action Idempotency（同 idempotency_key 二次 → 返回已执行结果，不重复执行）
- R4.2 Adapter Permission Snapshot（contract_permission_snapshot 记录当时权限依据）
- EX.1 Approval Signature 集成（有效签名 → 通过；无效 → denied）
- EX.2 Credential Broker 集成（credential 经 Broker 获取）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from approval_signature import sign_approval, verify_approval
from credential_broker import CredentialBroker
from execution_adapter import AdapterRequest, ExecutionAdapter
from execution_contract import ExecutionContractStore
from execution_identity import ExecutionIdentityStore


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def contract():
    store = ExecutionContractStore()
    c = store.create(
        plan_id="p", intent_id="i", run_id="r", requested_by="agent-1",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
    )
    c = store.approve(c.contract_id, approved_by="human-1")
    return store.activate(c.contract_id)


@pytest.fixture
def contract_store(contract):
    cs = ExecutionContractStore()
    cs._store[contract.contract_id] = contract
    return cs


@pytest.fixture
def adapter(contract_store):
    broker = CredentialBroker(contract_store=contract_store)
    return ExecutionAdapter(adapter_id="k8s-adapter-1", broker=broker)


def _req(**over):
    kw = dict(
        contract_id="c",
        credential_ref="cred::broker::c1",
        target={"namespace": "ns-a", "resource_type": "deployment", "resource_id": "checkout"},
        action="restart",
        params={},
        dry_run=False,
        idempotency_key="",
    )
    kw.update(over)
    return AdapterRequest(**kw)


# ═══════════════════════════════════════════════════════
#  R4.1 Idempotency
# ═══════════════════════════════════════════════════════

class TestR41Idempotency:
    def test_same_key_returns_cached(self, adapter, contract):
        req = _req(contract_id=contract.contract_id, idempotency_key="exec-request-1")
        r1 = adapter.execute(req, contract)
        r2 = adapter.execute(req, contract)
        assert r1.execution_trace_id == r2.execution_trace_id  # 相同结果（不重复执行）


# ═══════════════════════════════════════════════════════
#  R4.2 Permission Snapshot
# ═══════════════════════════════════════════════════════

class TestR42PermissionSnapshot:
    def test_permission_snapshot_recorded(self, adapter, contract):
        req = _req(contract_id=contract.contract_id)
        result = adapter.execute(req, contract)
        assert result.contract_permission_snapshot is not None
        assert result.contract_permission_snapshot["allowed_actions"] == ["restart"]
        assert result.contract_permission_snapshot["allowed_resources"] == ["ns-a"]


# ═══════════════════════════════════════════════════════
#  EX.1 Signature 集成
# ═══════════════════════════════════════════════════════

class TestSignatureIntegration:
    def test_valid_signature_allows(self, adapter, contract, contract_store):
        key = Ed25519PrivateKey.generate()
        sig = sign_approval(
            contract_fields={
                "contract_id": contract.contract_id,
                "actions": ["restart"],
                "resources": ["ns-a"],
                "expire_time": str(contract.expire_time),
            },
            signer="human-1",  # approved_by
            private_key=key,
            key_id="key-1", public_key_version="v1",
        )
        # 提供签名 + 验签器
        adapter._approval_verifier = (
            lambda s, cf, pk, es: verify_approval(s, contract_fields=cf, public_key=pk, expected_signer=es)
        )
        adapter._approval_public_key = key.public_key()
        req = _req(contract_id=contract.contract_id)
        result = adapter.execute(req, contract, approval_signature=sig)
        assert result.status == "success"

    def test_no_signature_denied_when_required(self, adapter, contract):
        # 设置要求签名 → 无签名拒绝
        adapter._require_signature = True
        req = _req(contract_id=contract.contract_id)
        result = adapter.execute(req, contract)
        assert result.status == "denied"


# ═══════════════════════════════════════════════════════
#  EX.2 Credential Broker 集成
# ═══════════════════════════════════════════════════════

class TestBrokerIntegration:
    def test_credential_via_broker(self, adapter, contract, contract_store):
        is_ = ExecutionIdentityStore(contract_store=contract_store)
        identity = is_.issue(
            contract_id=contract.contract_id, run_id=contract.run_id, executed_by="exec-1",
            identity_type="execution", principal_id="k8s-adapter-1",
            credential_ref="cred::broker::c1", scope="ns-a", expire_time=contract.expire_time,
        )
        req = _req(contract_id=contract.contract_id)
        result = adapter.execute(req, contract, execution_identity=identity)
        assert result.credential_id  # 经 Broker 获取的凭据 id
        assert result.status == "success"
