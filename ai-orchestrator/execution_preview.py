"""P8.5 ExecutionPreview / Simulation — V9.3 Phase8 真实执行前的无副作用验证。

核心原则（P8.5 v0.2）：
- preview 生成不触发任何真实副作用（纯只读模拟）。
- 只有 approved 的 preview 才允许 adapter 真实执行（P8.3）。
- Preview 保存环境状态快照（environment_snapshot/resource_version/expected_change/rollback_plan）——
  缺一 → 拒绝 approved（不具备审计价值）。
- Rollback 责任边界：Agent 提建议 / Human 批准 / Policy 检查 / Adapter 执行；禁 Agent 自动 rollback。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from execution_contract import ExecutionContract

PREVIEW_STATUSES = {"pending", "approved", "rejected"}


class PreviewNotApproved(ValueError):
    def __init__(self, message: str):
        self.error_code = "PREVIEW_NOT_APPROVED"
        super().__init__(message)


class PreviewRejected(ValueError):
    def __init__(self, message: str):
        self.error_code = "PREVIEW_REJECTED"
        super().__init__(message)


class PreviewDrift(ValueError):
    """执行对象漂移（R2/EX.5）：当前资源版本/cluster/namespace 与 Preview 记录不一致。"""

    def __init__(self, message: str):
        self.error_code = "PREVIEW_DRIFT"
        super().__init__(message)


@dataclass
class ExecutionPreview:
    preview_id: str
    contract_id: str
    target: Dict[str, Any]
    impact: str
    risk: str
    actions: List[str]
    environment_snapshot: Optional[Dict[str, Any]] = None
    resource_version: str = ""
    expected_change: Optional[Dict[str, Any]] = None
    rollback_plan: Optional[Dict[str, Any]] = None
    cluster_id: str = ""  # EX.5 防漂移
    namespace: str = ""  # EX.5 防漂移
    status: str = "pending"
    created_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))

    def __post_init__(self) -> None:
        if self.status not in PREVIEW_STATUSES:
            raise ValueError(f"非法 status: {self.status}")


class ExecutionPreviewStore:
    """内存 ExecutionPreview Store（MVP）。"""

    def __init__(self) -> None:
        self._store: Dict[str, ExecutionPreview] = {}

    def generate(
        self,
        *,
        contract,
        target: Dict[str, Any],
        impact: str,
        risk: str,
        actions: List[str],
        environment_snapshot: Optional[Dict[str, Any]] = None,
        resource_version: str = "",
        expected_change: Optional[Dict[str, Any]] = None,
        rollback_plan: Optional[Dict[str, Any]] = None,
        cluster_id: str = "",
        namespace: str = "",
        **kwargs,
    ) -> ExecutionPreview:
        """生成无副作用 preview。无 contract → 拒绝（不生成）。"""
        if contract is None:
            raise PreviewRejected("无 contract 不生成 preview")
        preview = ExecutionPreview(
            preview_id=str(uuid.uuid4()),
            contract_id=contract.contract_id,
            target=dict(target),
            impact=impact,
            risk=risk,
            actions=list(actions),
            environment_snapshot=dict(environment_snapshot) if environment_snapshot else None,
            resource_version=resource_version,
            expected_change=dict(expected_change) if expected_change else None,
            rollback_plan=dict(rollback_plan) if rollback_plan else None,
            cluster_id=cluster_id,
            namespace=namespace or (target or {}).get("namespace", ""),
            status="pending",
        )
        self._store[preview.preview_id] = preview
        return preview

    def verify_no_drift(
        self,
        preview_id: str,
        *,
        current_resource_version: str,
        current_cluster: str,
        current_namespace: str,
    ) -> bool:
        """执行前校验：当前资源版本/cluster/namespace 与 Preview 记录一致，防执行对象漂移（EX.5）。"""
        p = self._store.get(preview_id)
        if p is None:
            raise PreviewDrift(f"preview 不存在: {preview_id}")
        if p.resource_version and p.resource_version != current_resource_version:
            raise PreviewDrift(
                f"资源版本漂移: Preview={p.resource_version}, 当前={current_resource_version}"
            )
        if p.cluster_id and p.cluster_id != current_cluster:
            raise PreviewDrift(f"集群漂移: Preview={p.cluster_id}, 当前={current_cluster}")
        if p.namespace and p.namespace != current_namespace:
            raise PreviewDrift(f"namespace 漂移: Preview={p.namespace}, 当前={current_namespace}")
        return True

    def approve(self, preview_id: str) -> ExecutionPreview:
        """人工确认。缺状态快照 → 拒绝 approved。"""
        p = self._store.get(preview_id)
        if p is None:
            raise PreviewNotApproved(f"preview 不存在: {preview_id}")
        # 状态快照完整性（评审补充：缺一 → 不具备审计价值，拒绝 approved）
        if not p.environment_snapshot or not p.expected_change or not p.resource_version:
            raise PreviewRejected("preview 缺状态快照（environment_snapshot/expected_change/resource_version）")
        p.status = "approved"
        return p

    def reject(self, preview_id: str) -> ExecutionPreview:
        p = self._store.get(preview_id)
        if p is None:
            raise PreviewNotApproved(f"preview 不存在: {preview_id}")
        p.status = "rejected"
        return p

    def is_approved(self, preview_id: str) -> bool:
        p = self._store.get(preview_id)
        return p is not None and p.status == "approved"

    def get(self, preview_id: str) -> Optional[ExecutionPreview]:
        return self._store.get(preview_id)
