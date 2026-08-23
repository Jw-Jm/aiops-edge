"""P7.5 Intent Engine — V9.3 Phase7 规范化用户调查意图。

核心原则：
- 目标歧义 → RESOURCE_AMBIGUOUS（禁止猜）；证据不足 → 记录 missing，不强行归因。
- scope 收敛：tenant/cluster 必须 canonical；capability 来自 Tool Registry（LLM 不生成 capability）。
- action_mode：Phase7 仅 read_only/plan_only（execute_allowed 不启用）。
- source：user_explicit | approved_system_event（Alert/Event 仅预填 scope，不自动创建 Run）。
- Intent ≠ RunInvocation：Intent 不创建 Run；经用户批准后才进入 RunInvocation（P7.8）。
- 状态机：IntentCreated → EvidenceCollected → PlanGenerated → AwaitingApproval（Phase7 停此）。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional
from uuid import UUID

from tool_registry import KNOWN_CAPABILITIES

# §28.2 action_mode（Phase7 禁 execute_allowed）
ACTION_MODES = {"read_only", "plan_only", "execute_allowed"}

# §28.3 target_type（14 种）
TARGET_TYPES = {
    "cluster", "namespace", "node", "service", "deployment", "statefulset", "daemonset",
    "pod", "container", "workload", "host", "vm", "alert", "trace", "resource",
}

# 状态机（Phase7 停于 AwaitingApproval）
INTENT_STATUSES = {"IntentCreated", "EvidenceCollected", "PlanGenerated", "AwaitingApproval"}

# source 枚举（Alert/Event 只作预填，不创建 Run）
INTENT_SOURCES = {"user_explicit", "approved_system_event"}

# 需要明确 resource 标识的 target_type（cluster 级无需）
_RESOURCE_REQUIRED_TARGETS = {
    "namespace", "node", "service", "deployment", "statefulset", "daemonset", "pod",
    "container", "workload", "host", "vm", "alert", "trace", "resource",
}

_VALID_TRANSITIONS = {
    "IntentCreated": {"EvidenceCollected"},
    "EvidenceCollected": {"PlanGenerated"},
    "PlanGenerated": {"AwaitingApproval"},
    "AwaitingApproval": set(),
}


class IntentAmbiguityError(ValueError):
    """目标歧义（RESOURCE_AMBIGUOUS）。禁止猜。"""

    def __init__(self, reason: List[str]):
        self.error_code = "RESOURCE_AMBIGUOUS"
        self.reason = list(reason)
        super().__init__(f"RESOURCE_AMBIGUOUS: {', '.join(reason)}")


def _is_canonical(value) -> bool:
    if not value:
        return False
    try:
        return str(UUID(str(value))) == str(value)
    except (ValueError, AttributeError, TypeError):
        return False


@dataclass
class Intent:
    intent_id: str
    intent: str
    action_mode: str
    target_type: str
    tenant_id: str
    capability: str
    source: str
    status: str
    target_resource_id: Optional[str] = None
    scope_kind: str = "cluster"
    primary_cluster_id: Optional[str] = None
    time_range_start: Optional[str] = None
    time_range_end: Optional[str] = None
    symptom: str = ""
    ambiguity: Optional[Dict[str, Any]] = None
    evidence_gaps: List[str] = field(default_factory=list)

    def __post_init__(self) -> None:
        if self.action_mode not in ACTION_MODES:
            raise ValueError(f"非法 action_mode: {self.action_mode}")
        if self.target_type not in TARGET_TYPES:
            raise ValueError(f"非法 target_type: {self.target_type}")
        if self.status not in INTENT_STATUSES:
            raise ValueError(f"非法 status: {self.status}")
        if self.source not in INTENT_SOURCES:
            raise ValueError(f"非法 source: {self.source}")


class IntentEngine:
    """规范化用户调查意图的内存引擎（MVP）。"""

    def __init__(self) -> None:
        self._store: Dict[str, Intent] = {}

    def create_intent(
        self,
        *,
        intent: str,
        action_mode: str,
        target_type: str,
        tenant_id: str,
        capability: str,
        source: str = "user_explicit",
        target_resource_id: Optional[str] = None,
        primary_cluster_id: Optional[str] = None,
        time_range_start: Optional[str] = None,
        time_range_end: Optional[str] = None,
        symptom: str = "",
    ) -> Intent:
        """创建结构化 Intent。目标歧义 → RESOURCE_AMBIGUOUS（禁止猜）。"""
        # Phase7：execute_allowed 不启用
        if action_mode not in {"read_only", "plan_only"}:
            raise ValueError("Phase7 action_mode 仅 read_only/plan_only")
        if target_type not in TARGET_TYPES:
            raise ValueError(f"非法 target_type: {target_type}")
        # capability 只能来自 Tool Registry（LLM 不生成 capability）
        if capability not in KNOWN_CAPABILITIES:
            raise ValueError(f"capability 未注册/非法（仅来自 Tool Registry）: {capability}")
        if not _is_canonical(tenant_id):
            raise ValueError(f"tenant_id 非 canonical: {tenant_id}")
        if primary_cluster_id is not None and not _is_canonical(primary_cluster_id):
            raise ValueError(f"primary_cluster_id 非 canonical: {primary_cluster_id}")

        # 歧义检测：需 resource 的 target_type 缺 resource → RESOURCE_AMBIGUOUS（禁止猜）
        if target_type in _RESOURCE_REQUIRED_TARGETS and not target_resource_id:
            raise IntentAmbiguityError(["missing_resource"])
        # 缺 time_range → 提供默认（最近 1h），不猜测目标
        if not time_range_start and not time_range_end:
            time_range_start, time_range_end = "2026-08-20T00:00:00Z", "2026-08-20T01:00:00Z"

        it = Intent(
            intent_id=str(uuid.uuid4()),
            intent=intent,
            action_mode=action_mode,
            target_type=target_type,
            tenant_id=tenant_id,
            capability=capability,
            source=source,
            status="IntentCreated",
            target_resource_id=target_resource_id,
            scope_kind="cluster" if target_type == "cluster" else "cluster",
            primary_cluster_id=primary_cluster_id,
            time_range_start=time_range_start,
            time_range_end=time_range_end,
            symptom=symptom,
        )
        self._store[it.intent_id] = it
        return it

    def transition(self, intent_id: str, new_status: str) -> Intent:
        if new_status not in INTENT_STATUSES:
            raise ValueError(f"非法 status（Phase7 不进入 {new_status}）: {new_status}")
        it = self._store.get(intent_id)
        if it is None:
            raise KeyError(f"intent 不存在: {intent_id}")
        allowed = _VALID_TRANSITIONS.get(it.status, set())
        if new_status not in allowed:
            raise ValueError(f"非法迁移: {it.status} → {new_status}")
        it.status = new_status
        return it

    def get(self, intent_id: str) -> Optional[Intent]:
        return self._store.get(intent_id)
