"""PE.3 Credential Broker 生产校验 — TDD 测试（V9.3 Production Enablement）。

覆盖 PE.3 Security Review 清单：
- T1 TTL ≤ contract expire_time（不超限）
- T2 scope 不超 contract.allowed_resources/actions
- T3 revocation 立即失效
- T4 verify_production_constraints（生产准入校验）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from credential_broker import CredentialBroker
from execution_contract import ExecutionContractStore
from execution_identity import ExecutionIdentityStore


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def contract():
    store = ExecutionContractStore()
    return store.create(
        plan_id="p", intent_id="i", run_id="r", requested_by="a",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
    )


@pytest.fixture
def broker(contract):
    cs = ExecutionContractStore()
    cs._store[contract.contract_id] = contract
    return CredentialBroker(contract_store=cs)


def _delegate(broker, contract):
    is_ = ExecutionIdentityStore(contract_store=broker._contract_store)
    identity = is_.issue(
        contract_id=contract.contract_id, run_id=contract.run_id, executed_by="exec-1",
        identity_type="execution", principal_id="k8s-adapter-1",
        credential_ref="cred::broker::c1", scope="ns-a", expire_time=contract.expire_time,
    )
    return broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s")


# ═══════════════════════════════════════════════════════
#  T1 TTL ≤ contract expire
# ═══════════════════════════════════════════════════════

class TestT1TTL:
    def test_ttl_within_contract_expire(self, broker, contract):
        cred = _delegate(broker, contract)
        assert cred.expire_time <= contract.expire_time  # TTL 不超限


# ═══════════════════════════════════════════════════════
#  T2 scope 不超 contract
# ═══════════════════════════════════════════════════════

class TestT2Scope:
    def test_scope_within_contract(self, broker, contract):
        cred = _delegate(broker, contract)
        assert cred.scope["namespace"] in contract.allowed_resources
        assert set(cred.scope["actions"]).issubset(set(contract.allowed_actions))


# ═══════════════════════════════════════════════════════
#  T3 revocation 立即失效
# ═══════════════════════════════════════════════════════

class TestT3Revocation:
    def test_revoke_immediate(self, broker, contract):
        cred = _delegate(broker, contract)
        broker.revoke(cred.credential_id)
        assert broker.is_valid(cred.credential_id) is False  # 立即失效


# ═══════════════════════════════════════════════════════
#  T4 verify_production_constraints
# ═══════════════════════════════════════════════════════

class TestT4Constraints:
    def test_constraints_pass(self, broker, contract):
        cred = _delegate(broker, contract)
        assert broker.verify_production_constraints(contract, cred) is True

    def test_scope_exceeded_fails(self, broker, contract):
        cred = _delegate(broker, contract)
        cred.scope["namespace"] = "ns-x"  # 篡改为 contract 外 scope
        assert broker.verify_production_constraints(contract, cred) is False
