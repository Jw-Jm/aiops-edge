"""P8.3 Execution Adapter Boundary — TDD 测试（V9.3 Phase8，内存 MVP）。

覆盖 P8.3 设计 v0.2 的 T1-T6：
- T1 contract 未 active → denied
- T2 action 白名单外 → denied
- T3 target 资源范围外 → denied
- T4 无有效 credential → denied
- T5 dry_run 无副作用（不产生真实执行）
- T6 审计字段（adapter_id/target_snapshot/before_state/after_state/execution_trace_id）
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_adapter import AdapterRequest, ExecutionAdapter
from execution_contract import ExecutionContractStore


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def contract():
    store = ExecutionContractStore()
    c = store.create(
        plan_id="plan-1", intent_id="intent-1", run_id="run-1", requested_by="agent-1",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
    )
    c = store.approve(c.contract_id, approved_by="human-1")
    return store.activate(c.contract_id)


@pytest.fixture
def adapter():
    return ExecutionAdapter(adapter_id="k8s-adapter-1")


def _req(adapter, **over):
    kw = dict(
        contract_id="c",
        credential_ref="cred::broker::shortlived-1",
        target={"namespace": "ns-a", "resource_type": "deployment", "resource_id": "checkout"},
        action="restart",
        params={},
        dry_run=False,
    )
    kw.update(over)
    return AdapterRequest(**kw)


# ═══════════════════════════════════════════════════════
#  T1 contract 未 active
# ═══════════════════════════════════════════════════════

class TestT1ContractNotActive:
    def test_draft_contract_denied(self, adapter):
        store = ExecutionContractStore()
        c = store.create(
            plan_id="p", intent_id="i", run_id="r", requested_by="a",
            allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
            max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
        )
        req = _req(adapter, contract_id=c.contract_id)
        result = adapter.execute(req, c)  # draft → denied
        assert result.status == "denied"


# ═══════════════════════════════════════════════════════
#  T2 action 白名单外
# ═══════════════════════════════════════════════════════

class TestT2ActionOutside:
    def test_delete_denied(self, adapter, contract):
        req = _req(adapter, contract_id=contract.contract_id, action="delete")
        result = adapter.execute(req, contract)
        assert result.status == "denied"  # delete 不在 allowed_actions=["restart"]


# ═══════════════════════════════════════════════════════
#  T3 target 资源范围外
# ═══════════════════════════════════════════════════════

class TestT3TargetOutside:
    def test_namespace_outside_denied(self, adapter, contract):
        req = _req(adapter, contract_id=contract.contract_id, target={
            "namespace": "ns-b", "resource_type": "deployment", "resource_id": "x",
        })
        result = adapter.execute(req, contract)
        assert result.status == "denied"  # ns-b 不在 allowed_resources=["ns-a"]


# ═══════════════════════════════════════════════════════
#  T4 无有效 credential
# ═══════════════════════════════════════════════════════

class TestT4NoCredential:
    def test_missing_credential_denied(self, adapter, contract):
        req = _req(adapter, contract_id=contract.contract_id, credential_ref="")
        result = adapter.execute(req, contract)
        assert result.status == "denied"


# ═══════════════════════════════════════════════════════
#  T5 dry_run 无副作用
# ═══════════════════════════════════════════════════════

class TestT5DryRun:
    def test_dry_run_no_side_effect(self, adapter, contract):
        req = _req(adapter, contract_id=contract.contract_id, dry_run=True)
        result = adapter.execute(req, contract)
        assert result.status == "dry_run"
        assert result.after_state is None  # 无真实副作用


# ═══════════════════════════════════════════════════════
#  T6 审计字段
# ═══════════════════════════════════════════════════════

class TestT6AuditFields:
    def test_success_includes_audit(self, adapter, contract):
        req = _req(adapter, contract_id=contract.contract_id)
        result = adapter.execute(req, contract)
        assert result.status == "success"
        assert result.adapter_id == "k8s-adapter-1"
        assert result.target_snapshot is not None
        assert result.before_state is not None
        assert result.after_state is not None
        assert result.execution_trace_id
        assert result.rollback_ref
