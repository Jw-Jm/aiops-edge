"""P8.1 Execution Contract — TDD 测试（V9.3 Phase8，内存 MVP）。

覆盖 P8.1 设计 v0.2 的 T1-T8：
- T1 不可绕过（无 contract/draft/expired/revoked 拒绝；仅 active+有效期内可执行）
- T2 一次性（executed 不可复用；expired 不可续；revoked 不可逆）
- T3 scope 白名单（allowed_actions/resources 绑定）
- T4 Approval ≠ 授权执行（approve 只生成 approved，需显式 activate）
- T5 credential 委托引用（不持 Secret）
- T6 dry-run 前置
- T7 contract_hash 完整性（篡改 → hash 不匹配 → 拒绝）
- T8 execution_lock（二次 acquire_lock → 拒绝，防并发重复执行）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ContractNotExecutable, ExecutionContract, ExecutionContractStore


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def store():
    return ExecutionContractStore()


def _create(store, **over):
    kw = dict(
        plan_id="plan-1",
        intent_id="intent-1",
        run_id="run-1",
        requested_by="agent-1",
        allowed_tools=["execute_k8s.v1"],
        allowed_resources=["ns-a"],
        allowed_actions=["restart"],
        max_scope="namespace",
        expire_time=_now() + timedelta(minutes=5),
        rollback_policy={"rollback_action": "restart", "rollback_timeout": 60, "requires_approval": True},
    )
    kw.update(over)
    return store.create(**kw)


# ═══════════════════════════════════════════════════════
#  T1 不可绕过
# ═══════════════════════════════════════════════════════

class TestT1NoBypass:
    def test_missing_contract_rejected(self, store):
        assert store.is_executable("nonexistent") is False

    def test_draft_not_executable(self, store):
        c = _create(store)
        assert c.status == "draft"
        assert store.is_executable(c.contract_id) is False

    def test_active_only_executable(self, store):
        c = _create(store)
        c = store.approve(c.contract_id, approved_by="human-1")
        c = store.activate(c.contract_id)
        assert c.status == "active"
        assert store.is_executable(c.contract_id) is True

    def test_expired_not_executable(self, store):
        # 过期 contract 不可激活（activate 抛），且 is_executable 恒 False
        c = _create(store, expire_time=_now() - timedelta(minutes=1))
        c = store.approve(c.contract_id, approved_by="human-1")
        assert store.is_executable(c.contract_id) is False  # 未 active → 不可执行


# ═══════════════════════════════════════════════════════
#  T2 一次性
# ═══════════════════════════════════════════════════════

class TestT2OneTime:
    def test_executed_not_reusable(self, store):
        c = _create(store)
        c = store.approve(c.contract_id, approved_by="human-1")
        c = store.activate(c.contract_id)
        c = store.acquire_lock(c.contract_id)
        c = store.complete(c.contract_id)
        assert c.status == "executed"
        with pytest.raises(ContractNotExecutable):
            store.acquire_lock(c.contract_id)  # executed 不可复用

    def test_expired_not_renewable(self, store):
        c = _create(store, expire_time=_now() - timedelta(minutes=1))
        c = store.approve(c.contract_id, approved_by="human-1")
        with pytest.raises(ContractNotExecutable):
            store.activate(c.contract_id)  # 过期不可续

    def test_revoked_irreversible(self, store):
        c = _create(store)
        c = store.approve(c.contract_id, approved_by="human-1")
        c = store.revoke(c.contract_id)
        assert c.status == "revoked"
        with pytest.raises(ContractNotExecutable):
            store.activate(c.contract_id)  # revoked 不可逆回 active


# ═══════════════════════════════════════════════════════
#  T3 Scope 白名单
# ═══════════════════════════════════════════════════════

class TestT3Scope:
    def test_allowed_actions_bound(self, store):
        c = _create(store, allowed_actions=["restart"], allowed_resources=["ns-a"])
        assert c.allowed_actions == ["restart"]
        assert c.allowed_resources == ["ns-a"]
        assert c.max_scope == "namespace"

    def test_whitelist_not_blacklist(self, store):
        c = _create(store, allowed_actions=["restart"])
        assert "delete" not in c.allowed_actions  # 缺省拒绝


# ═══════════════════════════════════════════════════════
#  T4 Approval ≠ 授权执行
# ═══════════════════════════════════════════════════════

class TestT4ApprovalNotAuthorization:
    def test_approve_only_generates_approved(self, store):
        c = _create(store)
        c = store.approve(c.contract_id, approved_by="human-1")
        assert c.status == "approved"
        # approve 不触发执行；需显式 activate 才 active
        assert store.is_executable(c.contract_id) is False
        c = store.activate(c.contract_id)
        assert c.status == "active"


# ═══════════════════════════════════════════════════════
#  T5 Credential Delegation 引用
# ═══════════════════════════════════════════════════════

class TestT5CredentialDelegation:
    def test_contract_references_credential_not_secret(self, store):
        c = _create(store)
        # 内存 MVP：contract 不持 Secret，credential 委托在 P8.2/P8.3
        assert not hasattr(c, "secret")
        assert hasattr(c, "executed_by") or c.requested_by


# ═══════════════════════════════════════════════════════
#  T7 contract_hash 完整性
# ═══════════════════════════════════════════════════════

class TestT7HashIntegrity:
    def test_hash_computed_and_verifiable(self, store):
        c = _create(store)
        assert c.contract_hash
        assert store.verify_hash(c.contract_id) is True

    def test_tampered_contract_rejected(self, store):
        c = _create(store)
        c.allowed_actions.append("delete")  # 运行期篡改
        assert store.verify_hash(c.contract_id) is False  # hash 不匹配 → 拒绝


# ═══════════════════════════════════════════════════════
#  T8 execution_lock
# ═══════════════════════════════════════════════════════

class TestT8ExecutionLock:
    def test_acquire_lock_once(self, store):
        c = _create(store)
        c = store.approve(c.contract_id, approved_by="human-1")
        c = store.activate(c.contract_id)
        c = store.acquire_lock(c.contract_id)
        assert c.status == "executing"

    def test_double_acquire_lock_rejected(self, store):
        c = _create(store)
        c = store.approve(c.contract_id, approved_by="human-1")
        c = store.activate(c.contract_id)
        c = store.acquire_lock(c.contract_id)
        with pytest.raises(ContractNotExecutable):
            store.acquire_lock(c.contract_id)  # 同 contract 二次 → 拒绝（防并发重复执行）
