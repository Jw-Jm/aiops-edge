"""P7.6 Planner — V9.3 Phase7 唯一调查 DAG 控制器（只产出 Plan Proposal）。

核心原则（F4）：
- 只产出提案（PlanProposal），不输出最终 Root Cause；Planner → Executor 直连禁止。
- 预算硬约束：max_steps/max_tools/max_latency 冻结，超限 → budget_exceeded 终止。
- 依赖满足才执行 step；互不依赖分支可并行；依赖缺失 → failed（不误执行）。
- MissingEvidence 只生成 follow-up slot，不强行归因。
- requires_human_approval=true 强制；Phase7 停于 awaiting_approval。
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional
from uuid import UUID

import contracts
from contracts_identity import FROZEN_PLAN_STEP_NS, plan_step_id
from intent_engine import Intent
from tool_registry import ToolRegistry

PLAN_STATUSES = {"pending", "running", "completed", "awaiting_approval", "budget_exceeded", "failed"}
STEP_STATUSES = {"pending", "running", "completed", "skipped", "failed"}

# Y2：status 字符串 → 权威 PlanStepStatus 映射（lifecycle 由 RuntimeState 独立管理）。
# 注：权威 PlanStepStatus 无 SKIPPED（skipped 是 RuntimeState.outcome，非 PlanStep.status）。
_STATUS_TO_ENUM = {
    "pending": contracts.PlanStepStatus.PENDING,
    "running": contracts.PlanStepStatus.RUNNING,
    "completed": contracts.PlanStepStatus.SUCCESS,
    "failed": contracts.PlanStepStatus.FAILED,
}
_ENUM_TO_STATUS = {v: k for k, v in _STATUS_TO_ENUM.items()}


class FollowUpValidationError(ValueError):
    """Follow-up 追加步骤校验失败（未注册 Tool / capability 不匹配 / 预算超限）。"""

    def __init__(self, message: str):
        self.error_code = "FOLLOW_UP_VALIDATION_ERROR"
        super().__init__(message)


class PlanStepValidationError(ValueError):
    """Y2：PlanStep 构造校验失败（禁 assert，显式稳定错误码）。"""

    def __init__(self, code: str, message: str):
        self.error_code = code
        super().__init__(message)


class PlanStep:
    """Y2：组合权威 contracts.PlanStep + step_id 标签（非独立平行合同）。

    - contract: contracts.PlanStep（唯一权威 DAG 节点，UUID id）。
    - label: 原 step_id 字符串（仅显示别名，业务主键 = contract.id UUID）。
    - result_ref / is_followup: 运行态（Y2 中由 PlanStepRuntimeState 承载，此处保留兼容字段，
      最终迁移后移除）。
    """

    __slots__ = ("contract", "label", "result_ref", "is_followup", "label_index")

    def __init__(self, contract, label: str = "", result_ref: Any = None,
                 is_followup: bool = False, label_index: Optional[Dict[str, UUID]] = None):
        object.__setattr__(self, "contract", contract)
        object.__setattr__(self, "label", label)
        object.__setattr__(self, "result_ref", result_ref)
        object.__setattr__(self, "is_followup", is_followup)
        object.__setattr__(self, "label_index", label_index)

    @property
    def step_id(self) -> str:
        return self.label or str(self.contract.id)

    @property
    def id(self) -> UUID:
        return self.contract.id

    @property
    def tool_id(self) -> str:
        return self.contract.action

    @property
    def params(self) -> Dict[str, Any]:
        return self.contract.parameters

    @property
    def depends_on(self) -> List[str]:
        """对外返回依赖 label（经 label_index 反查，兼容旧调用方）。

        内部 DAG 判定用 contract.depends_on（UUID）。label_index 缺省时回退 UUID 字符串。
        """
        idx = self.label_index or {}
        result = []
        for d in self.contract.depends_on:
            resolved = next((lb for lb, u in idx.items() if str(u) == str(d)), None)
            result.append(resolved if resolved else str(d))
        return result

    @property
    def status(self) -> str:
        return _ENUM_TO_STATUS.get(self.contract.status, "pending")

    def _set_status(self, status: str) -> None:
        new = self.contract.model_copy(update={"status": _STATUS_TO_ENUM[status]})
        object.__setattr__(self, "contract", new)


@dataclass
class PlanResult:
    tool_results: List[Any] = field(default_factory=list)
    evidence: List[Any] = field(default_factory=list)
    missing_evidence: List[Dict[str, Any]] = field(default_factory=list)


@dataclass
class PlanProposal:
    plan_id: str
    intent_id: str
    status: str
    risk_level: str
    requires_human_approval: bool
    steps: List[PlanStep]
    budget: contracts.PlannerBudget  # R2 预算固化：权威模型
    result: PlanResult = field(default_factory=PlanResult)
    created_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    updated_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    # Y2：label→UUID 索引 + run_id（随 plan 持久化，供反查 / follow-up 复用），非 PlanStep 身份
    _step_label_index: Dict[str, UUID] = field(default_factory=dict, repr=False)
    _run_id: UUID = field(default=None, repr=False)  # type: ignore[assignment]


class Planner:
    """内存 MVP：生成/校验 PlanProposal + DAG 调度 + 预算 + approval boundary。

    不持有 executor、不触发执行（Planner → Executor 直连禁止）。
    """

    def __init__(
        self,
        *,
        max_steps: int = 10,
        max_tools: int = 20,
        max_latency_ms: int = 60000,
        registry=None,
        max_followup_rounds: int = 2,
        max_total_steps: int = 20,
    ) -> None:
        self._max_steps = max_steps
        self._max_tools = max_tools
        self._max_latency = max_latency_ms
        self._registry = registry or ToolRegistry
        self._plans: Dict[str, PlanProposal] = {}
        # follow-up 约束（评审：共享预算/轮数/总步数，不开第二 Planner）
        self.max_followup_rounds = max_followup_rounds
        self.max_total_steps = max_total_steps

    def propose_plan(self, intent: Intent, steps: List[dict],
                     run_id: Optional[Any] = None, plan_id: Optional[Any] = None) -> PlanProposal:
        """从 Intent 生成 PlanProposal（只产提案，无最终根因）。Y2：构造权威 InvestigationPlan。

        显式接收 run_id/plan_id（Y2 §2）；缺省时派生（run_id 从 intent.intent_id，plan_id 随机）。
        校验（重复 label/悬空依赖/环）→ 统一 label→UUID 映射 → 权威 contracts.PlanStep。
        """
        if not hasattr(intent, "intent_id"):
            raise ValueError("intent 必须是 Intent 对象")
        self._validate_dag(steps)

        # 校验重复 label（显式错误码，禁 assert）
        all_labels = [s["step_id"] for s in steps]
        if len(set(all_labels)) != len(all_labels):
            dup = {l for l in all_labels if all_labels.count(l) > 1}
            raise PlanStepValidationError("DUPLICATE_STEP_LABEL", f"重复 step label: {dup}")
        # 悬空依赖：缺失依赖的 step 标记 failed（不误执行），不产生悬空 UUID 边。
        # 注：Y2 阻断点 4 要求"防悬空边"——缺失依赖 step 不入 DAG 执行（failed），依赖边不建立。
        label_set = set(all_labels)

        # run_id / plan_id（Y2 §2b）
        run_uuid = _as_uuid(run_id) if run_id else uuid.uuid5(FROZEN_PLAN_STEP_NS, f"run:{intent.intent_id}")
        plan_uuid = _as_uuid(plan_id) if plan_id else uuid.uuid4()

        # 统一 label → UUID 映射
        label_index: Dict[str, UUID] = {}
        for s in steps:
            label_index[s["step_id"]] = plan_step_id(run_uuid, plan_uuid, s["step_id"])

        # 权威 InvestigationPlan 构造
        plan_steps = []
        step_statuses: Dict[str, str] = {}
        for spec in steps:
            step_id = spec["step_id"]
            tool_id = spec["tool_id"]
            tool = self._registry.get(tool_id)
            if tool is None or tool.lifecycle_status != "active":
                raise ValueError(f"未注册/非 active Tool: {tool_id}")
            deps = list(spec.get("depends_on", []))
            # 依赖缺失 → 该 step failed（不误执行）
            missing_dep = [d for d in deps if d not in label_set]
            st = "failed" if missing_dep else "pending"
            step_statuses[step_id] = st
            contract = contracts.PlanStep(
                id=label_index[step_id],
                run_id=run_uuid,
                agent=_agent_for(tool),
                action=tool_id,
                parameters=dict(spec.get("params", {})),
                # 缺失依赖不入 DAG 边（防悬空 UUID），step 已标记 failed 不执行
                depends_on=[label_index[d] for d in deps if d in label_index],
                required=True,
                cluster_id=None,
                status=_STATUS_TO_ENUM[st],
            )
            plan_steps.append(
                PlanStep(contract, label=step_id,
                         result_ref=None, is_followup=False, label_index=label_index)
            )

        consumed_tools = len({s["tool_id"] for s in steps})
        consumed_steps = len(steps)
        # R2 预算固化：权威 PlannerBudget（max_tools/max_latency 是 Planner 构造限制，不入 budget 字段）
        budget = contracts.PlannerBudget(
            max_steps=self._max_steps,
            max_followup_rounds=self.max_followup_rounds,
            consumed_steps=consumed_steps,
        )
        exceeded = consumed_steps > self._max_steps or consumed_tools > self._max_tools
        status = "budget_exceeded" if exceeded else "pending"

        plan = PlanProposal(
            plan_id=str(plan_uuid),
            intent_id=intent.intent_id,
            status=status,
            risk_level="R0",
            requires_human_approval=True,
            steps=plan_steps,
            budget=budget,
        )
        # Y2：label_index + run_id 随 plan 持久化（供反查 / follow-up 复用）
        plan._step_label_index = label_index
        plan._run_id = run_uuid
        self._plans[plan.plan_id] = plan
        return plan

    def ready_steps(self, plan_id: str) -> List[str]:
        """返回依赖满足且待执行的 step_ids（可并行执行）。

        Y2：DAG 内部用 UUID id 做依赖判定（depends_on 是 UUID），对外返回 step label。
        """
        plan = self._get(plan_id)
        status_by_id = {str(s.id): s.status for s in plan.steps}
        ready = []
        for s in plan.steps:
            if s.status != "pending":
                continue
            deps_ok = all(status_by_id.get(str(d)) == "completed" for d in s.contract.depends_on)
            if deps_ok:
                ready.append(s.step_id)
        return ready

    def complete_step(self, plan_id: str, step_id: str, result_ref: Any) -> PlanProposal:
        plan = self._get(plan_id)
        for s in plan.steps:
            if s.step_id == step_id or str(s.id) == str(step_id):
                s._set_status("completed")
                s.result_ref = getattr(result_ref, "tool_id", None) or str(result_ref)
                plan.result.tool_results.append(result_ref)
                plan.updated_at = datetime.now(timezone.utc)
        return plan

    def add_followup_step(
        self,
        plan_id: str,
        tool_id: str,
        capability: str,
        depends_on: List[str],
        params: Optional[Dict[str, Any]] = None,
    ) -> PlanStep:
        """在既有 Plan DAG 上追加一个 follow-up 调查步骤（评审：共享原 Planner）。

        - 共享原 Plan DAG：追加到 plan.steps，不开启第二调查图。
        - 共享预算：追加后 consumed_steps 不超 max_steps / max_total_steps。
        - Registry 校验：tool 必须注册且 active；capability 必须匹配 tool.required_capability。
        - 受 max_followup_rounds 约束（防止无限 follow-up）。
        """
        plan = self._get(plan_id)
        # Tool Registry 校验
        tool = self._registry.get(tool_id)
        if tool is None or tool.lifecycle_status != "active":
            raise FollowUpValidationError(f"未注册/非 active Tool: {tool_id}")
        if capability != tool.required_capability:
            raise FollowUpValidationError(
                f"capability {capability} != tool.required_capability {tool.required_capability}"
            )
        # follow-up 轮数：统计已有 follow-up step（依赖非空或带 followup 标记）
        followup_count = sum(
            1 for s in plan.steps if getattr(s, "is_followup", False)
        )
        if followup_count >= self.max_followup_rounds:
            raise FollowUpValidationError(
                f"follow-up 超过 max_followup_rounds={self.max_followup_rounds}"
            )
        # 预算：总步数不超 max_total_steps（含初始）
        new_total = plan.budget.consumed_steps + 1
        if new_total > self.max_total_steps:
            raise FollowUpValidationError(
                f"follow-up 超过 max_total_steps={self.max_total_steps}"
            )
        # 共享 max_steps 硬预算
        if new_total > self._max_steps:
            raise FollowUpValidationError(
                f"follow-up 超过 max_steps={self._max_steps}"
            )
        # Y2：原子构造权威 PlanStep + label_index + 依赖边（从 label_index 取值，防悬空）
        label = f"fu-{len(plan.steps) + 1}"
        run_uuid = _as_uuid(plan._run_id) if getattr(plan, "_run_id", None) else uuid.uuid4()
        plan_uuid = _as_uuid(plan.plan_id)
        # 依赖校验：depends_on 必须 ∈ 既有 label_index ∪ 新 label
        for d in depends_on:
            if d not in plan._step_label_index:
                raise FollowUpValidationError(f"follow-up 依赖悬空: {d}")
        step_id_uuid = plan_step_id(run_uuid, plan_uuid, label)
        contract = contracts.PlanStep(
            id=step_id_uuid,
            run_id=run_uuid,
            agent=_agent_for(tool),
            action=tool_id,
            parameters=dict(params or {}),
            depends_on=[plan._step_label_index[d] for d in depends_on],
            required=True,
            cluster_id=None,
            status=contracts.PlanStepStatus.PENDING,
        )
        step = PlanStep(contract, label=label, result_ref=None, is_followup=True,
                        label_index=plan._step_label_index)
        plan.steps.append(step)
        plan._step_label_index[label] = step_id_uuid
        plan.budget.consumed_steps = new_total
        plan.updated_at = datetime.now(timezone.utc)
        return step

    def add_missing_evidence(self, plan_id: str, tool_id: str, capability: str, reason: str) -> PlanProposal:
        plan = self._get(plan_id)
        plan.result.missing_evidence.append(
            {"tool_id": tool_id, "capability": capability, "reason": reason}
        )
        plan.updated_at = datetime.now(timezone.utc)
        return plan

    def finalize(self, plan_id: str) -> PlanProposal:
        """所有 step 完成后停止于 awaiting_approval（Phase7 边界）。"""
        plan = self._get(plan_id)
        plan.status = "awaiting_approval"
        plan.updated_at = datetime.now(timezone.utc)
        return plan

    def get(self, plan_id: str) -> Optional[PlanProposal]:
        return self._plans.get(plan_id)

    def _get(self, plan_id: str) -> PlanProposal:
        plan = self._plans.get(plan_id)
        if plan is None:
            raise KeyError(f"plan 不存在: {plan_id}")
        return plan

    @staticmethod
    def _validate_dag(steps: List[dict]) -> None:
        """校验 DAG：无环、依赖引用存在（缺失的依赖由 propose 阶段标记 failed）。"""
        step_ids = {s["step_id"] for s in steps}
        # 检测环（DFS 三色）
        WHITE, GRAY, BLACK = 0, 1, 2
        color = {sid: WHITE for sid in step_ids}
        graph = {s["step_id"]: [d for d in s.get("depends_on", []) if d in step_ids] for s in steps}

        def dfs(node):
            color[node] = GRAY
            for nxt in graph.get(node, []):
                if color[nxt] == GRAY:
                    raise ValueError(f"DAG 检测到环: {node} -> {nxt}")
                if color[nxt] == WHITE:
                    dfs(nxt)
            color[node] = BLACK

        for sid in step_ids:
            if color[sid] == WHITE:
                dfs(sid)


def _as_uuid(value: Any) -> UUID:
    """把 run_id/plan_id 规范为 UUID：已是 UUID 用小写，否则 UUIDv5 派生。"""
    if isinstance(value, UUID):
        return value
    try:
        return UUID(str(value))
    except (ValueError, TypeError):
        return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"id:{value}")


def _agent_for(tool) -> str:
    """从 ToolRegistry 权威映射 agent（禁默认猜测）。"""
    agent_type = getattr(tool, "agent_type", None)
    if agent_type:
        return agent_type
    # 按 capability 派生（ToolRegistry 已校验 capability 归属）
    capability = getattr(tool, "required_capability", "") or ""
    domain = capability.split(".")[0] if "." in capability else capability
    return domain or "planner"
