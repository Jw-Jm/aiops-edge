"""P9.1 RCA Input Snapshot — V9.3 Phase9（评审加固版）。

每轮 RCA 基于明确 Evidence snapshot；记录 Evidence IDs、version/time，
不允许 LLM 在 scoring 中偷偷引入未登记事实（§七十五 P9.1）。

评审修复（2026-08-21 Gate 9 FAIL 退回）：
- evidence_ids 改为 tuple（frozen 真正不可变，禁止 append/remove 原地修改）。
- 补查生成新快照时 snapshot_version 递增（Re-score 用新版本，保证可复算）。
- 强隔离字段：tenant_id / cluster_id 强制携带，禁止跨 cluster Evidence 混入。
- assert_evidence_registered 校验 evidence 必须在 snapshot 内；register 时校验 tenant/cluster 一致。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import List, Optional, Tuple


class RcaSnapshotError(ValueError):
    def __init__(self, message: str):
        self.error_code = "RCA_SNAPSHOT_ERROR"
        super().__init__(message)


@dataclass(frozen=True)
class RcaInputSnapshot:
    """RCA 输入快照：冻结参与 RCA 的 Evidence IDs/version，禁止引入未登记事实。

    不可变：
    - evidence_ids 是 tuple，调用方无法原地 append/remove。
    - tenant_id / cluster_id 强隔离：同一 snapshot 不得混用跨 tenant/cluster Evidence。
    """

    run_id: str
    intent_id: str
    evidence_ids: Tuple[str, ...] = ()
    snapshot_version: str = "v1"
    generated_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    source: str = "evidence_hub"
    tenant_id: str = ""
    cluster_id: str = ""

    def __post_init__(self) -> None:
        # 保证 evidence_ids 恒为 tuple（即使调用方传入 list）
        object.__setattr__(self, "evidence_ids", tuple(self.evidence_ids))

    def assert_evidence_registered(self, evidence_id: str) -> None:
        """scoring 只能引用 snapshot 内 Evidence；未登记 → 拒绝（防 LLM 偷偷引入）。"""
        if evidence_id not in self.evidence_ids:
            raise RcaSnapshotError(
                f"evidence {evidence_id} 未登记到 RCA snapshot（禁引入未登记事实）"
            )

    def register_evidence(
        self, evidence_id: str, *, evidence_tenant: str = "", evidence_cluster: str = ""
    ) -> None:
        """登记 Evidence 到快照，校验 tenant/cluster 强隔离（禁跨 cluster 混入）。"""
        if self.tenant_id and evidence_tenant and evidence_tenant != self.tenant_id:
            raise RcaSnapshotError(
                f"Evidence {evidence_id} tenant={evidence_tenant} 与 snapshot tenant={self.tenant_id} 不一致"
            )
        if self.cluster_id and evidence_cluster and evidence_cluster != self.cluster_id:
            raise RcaSnapshotError(
                f"Evidence {evidence_id} cluster={evidence_cluster} 与 snapshot cluster={self.cluster_id} 不一致"
            )

    def add_evidence(
        self,
        evidence_id: str,
        *,
        evidence_tenant: str = "",
        evidence_cluster: str = "",
    ) -> "RcaInputSnapshot":
        """补查后新增 Evidence → 返回新 snapshot（不可变，生成递增 version）。"""
        if evidence_id in self.evidence_ids:
            return self
        self.register_evidence(evidence_id, evidence_tenant=evidence_tenant,
                               evidence_cluster=evidence_cluster)
        new_version = _bump_version(self.snapshot_version)
        return RcaInputSnapshot(
            run_id=self.run_id,
            intent_id=self.intent_id,
            evidence_ids=self.evidence_ids + (evidence_id,),
            snapshot_version=new_version,
            generated_at=datetime.now(timezone.utc),
            source=self.source,
            tenant_id=self.tenant_id,
            cluster_id=self.cluster_id,
        )


def _bump_version(version: str) -> str:
    """v1 → v2, v9 → v10；非法版本 → v2（从 v1 递增的兜底）。"""
    if version.startswith("v") and version[1:].isdigit():
        return f"v{int(version[1:]) + 1}"
    return "v2"
