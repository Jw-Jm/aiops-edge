"""EX.5 Preview 资源版本绑定（R2）— TDD 测试（V9.3 Execution Infrastructure）。

覆盖 EX.5 防执行对象漂移：
- T1 当前资源版本 ≠ Preview.resource_version → 拒绝执行（防漂移）
- T2 cluster/namespace 与 Preview 不符 → 拒绝
- T3 版本一致 → 允许执行
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from execution_contract import ExecutionContractStore
from execution_preview import ExecutionPreviewStore, PreviewDrift


def _now():
    return datetime.now(timezone.utc)


@pytest.fixture
def contract():
    store = ExecutionContractStore()
    c = store.create(
        plan_id="p", intent_id="i", run_id="r", requested_by="a",
        allowed_tools=["execute_k8s.v1"], allowed_resources=["ns-a"], allowed_actions=["restart"],
        max_scope="namespace", expire_time=_now() + timedelta(minutes=5), rollback_policy={},
    )
    return store.approve(c.contract_id, approved_by="human-1")


@pytest.fixture
def preview_store():
    return ExecutionPreviewStore()


def _preview(preview_store, contract, **over):
    kw = dict(
        contract=contract,
        target={"namespace": "ns-a", "resource_type": "deployment", "resource_id": "checkout"},
        impact="1 pod restart",
        risk="medium",
        actions=["restart"],
        environment_snapshot={"namespace": "ns-a"},
        resource_version="deployment-v5",
        expected_change={"restartCount": "3→4"},
        rollback_plan={"available": True},
        cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        namespace="ns-a",
    )
    kw.update(over)
    return preview_store.generate(**kw)


# ═══════════════════════════════════════════════════════
#  T1 版本不一致 → 拒绝
# ═══════════════════════════════════════════════════════

class TestT1VersionDrift:
    def test_version_mismatch_rejected(self, preview_store, contract):
        p = _preview(preview_store, contract, resource_version="deployment-v5")
        # 当前版本漂移 → 拒绝执行
        with pytest.raises(PreviewDrift):
            preview_store.verify_no_drift(
                p.preview_id,
                current_resource_version="deployment-v9",
                current_cluster="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                current_namespace="ns-a",
            )


# ═══════════════════════════════════════════════════════
#  T2 cluster/namespace 不符 → 拒绝
# ═══════════════════════════════════════════════════════

class TestT2ClusterNamespaceMismatch:
    def test_cluster_mismatch_rejected(self, preview_store, contract):
        p = _preview(preview_store, contract, cluster_id="cluster-A")
        with pytest.raises(PreviewDrift):
            preview_store.verify_no_drift(
                p.preview_id,
                current_resource_version="deployment-v5",
                current_cluster="cluster-B",  # 集群漂移
                current_namespace="ns-a",
            )

    def test_namespace_mismatch_rejected(self, preview_store, contract):
        p = _preview(preview_store, contract, namespace="ns-a")
        with pytest.raises(PreviewDrift):
            preview_store.verify_no_drift(
                p.preview_id,
                current_resource_version="deployment-v5",
                current_cluster="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
                current_namespace="ns-b",  # namespace 漂移
            )


# ═══════════════════════════════════════════════════════
#  T3 版本一致 → 允许
# ═══════════════════════════════════════════════════════

class TestT3NoDrift:
    def test_version_match_allows(self, preview_store, contract):
        p = _preview(preview_store, contract, resource_version="deployment-v5")
        ok = preview_store.verify_no_drift(
            p.preview_id,
            current_resource_version="deployment-v5",
            current_cluster="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            current_namespace="ns-a",
        )
        assert ok is True
