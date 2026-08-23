"""P9.1 RCA Input Snapshot — V9.3 Phase9（评审加固后测试）。

每轮 RCA 基于明确 Evidence snapshot；记录 Evidence IDs、version/time，
不允许 LLM 在 scoring 中偷偷引入未登记事实（§七十五 P9.1）。

评审加固断言：
- evidence_ids 不可变（tuple），无法原地 append/remove。
- 补查生成新快照时 version 递增。
- tenant/cluster 强隔离：禁跨 cluster Evidence 混入。
"""
import pytest


def test_snapshot_records_evidence_ids_and_version():
    from rca_snapshot import RcaInputSnapshot

    snap = RcaInputSnapshot(
        run_id="run-1",
        intent_id="intent-1",
        evidence_ids=["ev-1", "ev-2"],
        snapshot_version="v1",
    )
    assert snap.run_id == "run-1"
    assert snap.evidence_ids == ("ev-1", "ev-2")
    assert snap.snapshot_version == "v1"
    assert snap.source == "evidence_hub"
    assert snap.generated_at is not None


def test_snapshot_rejects_evidence_not_in_snapshot():
    from rca_snapshot import RcaInputSnapshot, RcaSnapshotError

    snap = RcaInputSnapshot(
        run_id="run-1",
        intent_id="intent-1",
        evidence_ids=["ev-1"],
        snapshot_version="v1",
    )
    with pytest.raises(RcaSnapshotError):
        snap.assert_evidence_registered("ev-999")


def test_snapshot_accepts_registered_evidence():
    from rca_snapshot import RcaInputSnapshot

    snap = RcaInputSnapshot(
        run_id="run-1",
        intent_id="intent-1",
        evidence_ids=["ev-1", "ev-2"],
        snapshot_version="v1",
    )
    snap.assert_evidence_registered("ev-2")  # 不抛异常


def test_snapshot_generated_at_is_time():
    from rca_snapshot import RcaInputSnapshot

    snap = RcaInputSnapshot(
        run_id="run-1",
        intent_id="intent-1",
        evidence_ids=["ev-1"],
        snapshot_version="v1",
    )
    assert hasattr(snap.generated_at, "isoformat")


# ---- 评审加固断言 ----

def test_snapshot_evidence_ids_are_immutable_tuple():
    from rca_snapshot import RcaInputSnapshot

    snap = RcaInputSnapshot(
        run_id="run-1", intent_id="intent-1", evidence_ids=["ev-1", "ev-2"], snapshot_version="v1",
    )
    # 是 tuple 而非 list
    assert isinstance(snap.evidence_ids, tuple)
    # 无法原地 append/remove（不可变）
    with pytest.raises(AttributeError):
        snap.evidence_ids.append("ev-3")  # type: ignore[attr-defined]
    with pytest.raises(AttributeError):
        snap.evidence_ids.remove("ev-1")  # type: ignore[attr-defined]


def test_snapshot_add_evidence_bumps_version():
    from rca_snapshot import RcaInputSnapshot

    snap = RcaInputSnapshot(
        run_id="run-1", intent_id="intent-1", evidence_ids=["ev-1"], snapshot_version="v1",
    )
    new = snap.add_evidence("ev-2")
    assert new.snapshot_version == "v2"
    assert new.evidence_ids == ("ev-1", "ev-2")
    # 原 snapshot 不变（不可变）
    assert snap.evidence_ids == ("ev-1",)
    assert snap.snapshot_version == "v1"


def test_snapshot_rejects_cross_cluster_evidence():
    from rca_snapshot import RcaInputSnapshot, RcaSnapshotError

    snap = RcaInputSnapshot(
        run_id="run-1", intent_id="intent-1", evidence_ids=["ev-1"],
        snapshot_version="v1", cluster_id="cluster-1",
    )
    with pytest.raises(RcaSnapshotError):
        snap.add_evidence("ev-cross", evidence_cluster="cluster-2")


def test_snapshot_accepts_same_cluster_evidence():
    from rca_snapshot import RcaInputSnapshot

    snap = RcaInputSnapshot(
        run_id="run-1", intent_id="intent-1", evidence_ids=["ev-1"],
        snapshot_version="v1", cluster_id="cluster-1",
    )
    new = snap.add_evidence("ev-2", evidence_cluster="cluster-1")
    assert "ev-2" in new.evidence_ids
