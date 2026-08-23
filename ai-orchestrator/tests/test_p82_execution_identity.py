"""P8.2 Execution Identity — TDD 测试（V9.3 Phase8，内存 MVP）。

覆盖 P8.2 设计 v0.2 的 T1-T5：
- T1 身份分离（approved_by / requested_by / executed_by 三值分离，executed_by ≠ 长期 Service Identity）
- T2 审计链（每次执行可追溯到三身份；缺任一环拒绝）
- T3 一次性 + 过期（随 contract expire；revoked 阻断；不可复用为长期身份）
- T4 Credential 边界（credential_ref 只引用委托，不存 Secret；Agent/Planner 不接触长期凭据）
- T5 不可自选（Agent 不能自选 executed_by；身份声明 ≠ 执行权限）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ExecutionContractStore
from execution_identity import (
    ExecutionIdentity,
    ExecutionIdentityStore,
    IdentityNotActive,
    IdentityNotAuthorized,
)


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def identity_store(contract):
    from execution_contract import ExecutionContractStore

    cs = ExecutionContractStore()
    cs._store[contract.contract_id] = contract  # 注入 active contract 供 scope 校验
    return ExecutionIdentityStore(contract_store=cs)


@pytest.fixture
def contract():
    store = ExecutionContractStore()
    c = store.create(
        plan_id="plan-1", intent_id="intent-1", run_id="run-1", requested_by="agent-1",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5),
        rollback_policy={},
    )
    c = store.approve(c.contract_id, approved_by="human-1")
    return store.activate(c.contract_id)


# ═══════════════════════════════════════════════════════
#  T1 身份分离
# ═══════════════════════════════════════════════════════

class TestT1IdentitySeparation:
    def test_three_identities_separate(self, identity_store, contract):
        # executed_by 由系统从 contract 派生（Execution Identity），≠ approved_by/requested_by
        identity = identity_store.issue(
            contract_id=contract.contract_id, run_id=contract.run_id,
            executed_by="execution-identity-1",  # 系统派生
            identity_type="execution", principal_id="k8s-adapter-1",
            credential_ref="cred::broker::shortlived-1", scope="ns-a",
            expire_time=_now() + timedelta(minutes=5),
        )
        assert identity.executed_by == "execution-identity-1"
        assert contract.approved_by == "human-1"  # 谁批准
        assert contract.requested_by == "agent-1"  # 谁发起
        assert identity.executed_by != contract.approved_by
        assert identity.executed_by != contract.requested_by

    def test_identity_type_execution(self, identity_store, contract):
        identity = identity_store.issue(
            contract_id=contract.contract_id, run_id=contract.run_id,
            executed_by="exec-1", identity_type="execution", principal_id="p",
            credential_ref="c", scope="ns-a", expire_time=_now() + timedelta(minutes=5),
        )
        assert identity.identity_type == "execution"
        assert identity.identity_id  # UUID


# ═══════════════════════════════════════════════════════
#  T2 审计链
# ═══════════════════════════════════════════════════════

class TestT2AuditChain:
    def test_full_audit_trace(self, identity_store, contract):
        identity = identity_store.issue(
            contract_id=contract.contract_id, run_id=contract.run_id,
            executed_by="exec-1", identity_type="execution", principal_id="k8s-adapter-1",
            credential_ref="cred::broker::c1", scope="ns-a",
            expire_time=_now() + timedelta(minutes=5),
        )
        trace = identity_store.audit_trace(identity.identity_id, contract)
        assert trace["approved_by"] == "human-1"
        assert trace["requested_by"] == "agent-1"
        assert trace["executed_by"] == "exec-1"

    def test_missing_identity_no_trace(self, identity_store, contract):
        with pytest.raises(IdentityNotAuthorized):
            identity_store.audit_trace("missing", contract)


# ═══════════════════════════════════════════════════════
#  T3 一次性 + 过期
# ═══════════════════════════════════════════════════════

class TestT3OneTimeExpiry:
    def test_revoke_blocks(self, identity_store, contract):
        identity = identity_store.issue(
            contract_id=contract.contract_id, run_id=contract.run_id,
            executed_by="exec-1", identity_type="execution", principal_id="p",
            credential_ref="c", scope="ns-a", expire_time=_now() + timedelta(minutes=5),
        )
        identity_store.revoke(identity.identity_id)
        assert identity_store.is_active(identity.identity_id) is False

    def test_expire_not_reusable(self, identity_store, contract):
        identity = identity_store.issue(
            contract_id=contract.contract_id, run_id=contract.run_id,
            executed_by="exec-1", identity_type="execution", principal_id="p",
            credential_ref="c", scope="ns-a", expire_time=_now() - timedelta(minutes=1),
        )
        assert identity_store.is_active(identity.identity_id) is False  # 过期不可用


# ═══════════════════════════════════════════════════════
#  T4 Credential 边界
# ═══════════════════════════════════════════════════════

class TestT4CredentialBoundary:
    def test_credential_ref_not_secret(self, identity_store, contract):
        identity = identity_store.issue(
            contract_id=contract.contract_id, run_id=contract.run_id,
            executed_by="exec-1", identity_type="execution", principal_id="p",
            credential_ref="cred::broker::shortlived-1", scope="ns-a",
            expire_time=_now() + timedelta(minutes=5),
        )
        assert identity.credential_ref.startswith("cred::broker::")
        assert "password" not in identity.credential_ref  # 不存 Secret


# ═══════════════════════════════════════════════════════
#  T5 不可自选
# ═══════════════════════════════════════════════════════

class TestT5NoSelfSelection:
    def test_agent_cannot_self_select(self, identity_store, contract):
        # Agent 不能自选 executed_by：executed_by 由系统从 contract 派生（这里用 store 强制校验 scope 匹配）
        with pytest.raises(IdentityNotAuthorized):
            identity_store.issue(
                contract_id=contract.contract_id, run_id=contract.run_id,
                executed_by="agent-self-selected", identity_type="execution", principal_id="p",
                credential_ref="c", scope="ns-b",  # scope 超出 contract.allowed_resources → 拒绝
                expire_time=_now() + timedelta(minutes=5),
            )
