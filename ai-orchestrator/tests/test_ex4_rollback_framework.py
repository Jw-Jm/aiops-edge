"""EX.4 Rollback Framework — TDD 测试（V9.3 Execution Infrastructure）。

覆盖 EX.4 + 评审补充（rollback_contract_id）：
- T1 无 Human 批准 → 拒绝 rollback
- T2 无 before_state → 拒绝 rollback（无法确定回滚目标）
- T3 rollback 经 Policy 检查
- T4 rollback_contract_id（不复用原 contract，Rollback 是新动作）；Agent 不能自动 rollback
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ExecutionContractStore
from rollback_framework import RollbackDenied, RollbackFramework


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
def framework():
    return RollbackFramework()


def _before_state():
    return {"namespace": "ns-a", "resource_id": "checkout", "replicas": 3}


# ═══════════════════════════════════════════════════════
#  T1 无 Human 批准 → 拒绝
# ═══════════════════════════════════════════════════════

class TestT1NoHumanApproval:
    def test_unapproved_rollback_denied(self, framework, contract):
        req = framework.request_rollback(
            original_contract=contract, before_state=_before_state(), requested_by="agent-1"
        )
        # 未 approve → execute 拒绝
        with pytest.raises(RollbackDenied):
            framework.execute_rollback(req.request_id)


# ═══════════════════════════════════════════════════════
#  T2 无 before_state → 拒绝
# ═══════════════════════════════════════════════════════

class TestT2NoBeforeState:
    def test_missing_before_state_rejected(self, framework, contract):
        with pytest.raises(RollbackDenied):
            framework.request_rollback(
                original_contract=contract, before_state=None, requested_by="agent-1"
            )


# ═══════════════════════════════════════════════════════
#  T3 rollback 经 Policy 检查
# ═══════════════════════════════════════════════════════

class TestT3PolicyCheck:
    def test_policy_deny_blocks_rollback(self, framework, contract):
        req = framework.request_rollback(
            original_contract=contract, before_state=_before_state(), requested_by="agent-1"
        )
        framework.approve(req.request_id, approved_by="human-1")
        # policy 拒绝 → rollback 不执行
        with pytest.raises(RollbackDenied):
            framework.execute_rollback(req.request_id, policy_allows=False)

    def test_policy_allow_executes(self, framework, contract):
        req = framework.request_rollback(
            original_contract=contract, before_state=_before_state(), requested_by="agent-1"
        )
        framework.approve(req.request_id, approved_by="human-1")
        ok = framework.execute_rollback(req.request_id, policy_allows=True)
        assert ok is True


# ═══════════════════════════════════════════════════════
#  T4 rollback_contract_id + Agent 不能自动 rollback
# ═══════════════════════════════════════════════════════

class TestT4RollbackContractId:
    def test_new_rollback_contract_id(self, framework, contract):
        req = framework.request_rollback(
            original_contract=contract, before_state=_before_state(), requested_by="agent-1"
        )
        # Rollback 是新动作：rollback_contract_id ≠ 原 contract_id
        assert req.rollback_contract_id != contract.contract_id

    def test_agent_cannot_auto_rollback(self, framework, contract):
        # request_rollback 后必须 Human approve 才能执行（Agent 不能自动）
        req = framework.request_rollback(
            original_contract=contract, before_state=_before_state(), requested_by="agent-1"
        )
        assert req.status == "pending_approval"
        framework.approve(req.request_id, approved_by="human-1")
        assert req.status == "approved"
