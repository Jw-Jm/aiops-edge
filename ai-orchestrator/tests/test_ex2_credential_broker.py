"""EX.2 Credential Broker — TDD 测试（V9.3 Execution Infrastructure）。

覆盖 EX.2 + R4.3（Credential Broker Audit）：
- T1 Broker 返回 short-lived 凭据（非长期）
- T2 凭据随 contract expire_time → 失效
- T3 contract revoked → Broker revoke → 凭据失效
- T4 Agent/Planner/Evidence 无凭据访问路径（Broker 唯一接触点，凭据对象只含引用+最小权限）
- T5 最小权限 scope（audience/scope 含 namespace/actions，非全 cluster admin）
- R4.3 audit：credential_issue_event 记录 who/when/contract/adapter/scope/expire
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
        plan_id="p", intent_id="i", run_id="r", requested_by="agent-1",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
    )


@pytest.fixture
def broker(contract):
    cs = ExecutionContractStore()
    cs._store[contract.contract_id] = contract
    return CredentialBroker(contract_store=cs)


def _identity(contract, broker):
    is_ = ExecutionIdentityStore(contract_store=broker._contract_store)
    return is_.issue(
        contract_id=contract.contract_id, run_id=contract.run_id, executed_by="exec-1",
        identity_type="execution", principal_id="k8s-adapter-1",
        credential_ref="cred::broker::c1", scope="ns-a", expire_time=contract.expire_time,
    )


# ═══════════════════════════════════════════════════════
#  T1 short-lived
# ═══════════════════════════════════════════════════════

class TestT1ShortLived:
    def test_delegate_returns_shortlived(self, broker, contract):
        identity = _identity(contract, broker)
        cred = broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s-adapter-1")
        assert cred.expire_time is not None  # 有生命周期
        assert broker.is_valid(cred.credential_id) is True


# ═══════════════════════════════════════════════════════
#  T2 凭据随 contract expire
# ═══════════════════════════════════════════════════════

class TestT2Expire:
    def test_credential_invalid_after_expire(self, broker, contract):
        identity = _identity(contract, broker)
        cred = broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s")
        # 手动把 expire_time 移到过去（模拟 contract 过期）
        cred.expire_time = _now() - timedelta(minutes=1)
        assert broker.is_valid(cred.credential_id) is False


# ═══════════════════════════════════════════════════════
#  T3 revoke
# ═══════════════════════════════════════════════════════

class TestT3Revoke:
    def test_revoked_credential_invalid(self, broker, contract):
        identity = _identity(contract, broker)
        cred = broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s")
        broker.revoke(cred.credential_id)
        assert broker.is_valid(cred.credential_id) is False


# ═══════════════════════════════════════════════════════
#  T4 Broker 唯一接触点（凭据对象只含引用+最小权限）
# ═══════════════════════════════════════════════════════

class TestT4SingleContact:
    def test_no_secret_in_credential(self, broker, contract):
        identity = _identity(contract, broker)
        cred = broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s")
        # 凭据对象不含明文 Secret
        assert not hasattr(cred, "password")
        assert not hasattr(cred, "kubeconfig_content")


# ═══════════════════════════════════════════════════════
#  T5 最小权限 scope
# ═══════════════════════════════════════════════════════

class TestT5LeastPrivilege:
    def test_scope_from_contract_whitelist(self, broker, contract):
        identity = _identity(contract, broker)
        cred = broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s")
        assert cred.scope["namespace"] == "ns-a"  # 来自 contract.allowed_resources
        assert cred.scope["actions"] == ["restart"]  # 来自 contract.allowed_actions
        # 非全 cluster admin
        assert cred.scope["cluster"] is False or "ns-a" in cred.scope["namespaces"]


# ═══════════════════════════════════════════════════════
#  R4.3 Credential Broker Audit
# ═══════════════════════════════════════════════════════

class TestR43Audit:
    def test_issue_event_recorded(self, broker, contract):
        identity = _identity(contract, broker)
        broker.delegate(contract=contract, execution_identity=identity, adapter_id="k8s-adapter-1")
        events = broker.audit_events()
        assert len(events) == 1
        ev = events[0]
        assert ev["contract_id"] == contract.contract_id
        assert ev["adapter_id"] == "k8s-adapter-1"
        assert ev["who"] == "exec-1"
        assert ev["scope"] == {"namespace": "ns-a", "actions": ["restart"]}
        assert ev["expire"] is not None
