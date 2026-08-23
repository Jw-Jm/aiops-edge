"""V9.3 Phase 11 — Remediation / Risk / Confirmation / Approval / Execution / Verification。

合同 §七十七（P11.1-P11.10）：
- P11.1 Structured OpsAction Factory：Hypothesis/RCA 达到允许条件才形成 action；
  id/version/hash/scope/idempotency_key/resource_version/expected_effect/verification_policy 必须齐全。
- P11.2 Execution Policy Engine：query-api 基于 action/parameter/RCA confidence/blast radius/environment/
  resource type 计算 authoritative_risk，永远不低于 baseline；继续 R0-R4，不新增 L0-L4 Autonomy。
- P11.3 Confirmation：R2 requester 显式确认；绑定 action hash/version/target/risk/resourceVersion；修改需重确认。
- P11.4 Approval：R3/R4 独立 approver；requester!=approver（admin 也不例外）；绑定 immutable action identity；
  cross-cluster approval 拒绝。
- P11.5 Precheck：执行前重新获取 current authorization/target identity/resourceVersion/current health/
  conflicting action/maintenance；任一不满足不执行。
- P11.6 Execution Adapter：固定 query-api security module；只接受 structured action；禁 raw LLM shell。
- P11.7 patch_resource / restricted_shell：patch 只允许明确资源/字段；restricted_shell 仍 R4、planner_selectable=false、
  automatic=false，不得成为 action failure fallback。
- P11.8 Observation/Verification：before→execute→observe→after→compare→verdict；退出码只是 fact，不是 recovery verdict。
- P11.9 Regression Stop：regressed 后立即停止后续自动 action，要求人工重新调查或新 Run；禁止自动连续试错。
- P11.10 Rollback as New Action：rollback 生成新 action_id/version/hash/risk/approval/execution/verification。

边界：In-memory MVP（不接真实 K8s/OpenStack/credential/生产系统）；Execution Production Execution 仍 NOT APPROVED。
"""
from __future__ import annotations

import hashlib
import json
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any


class Phase11Error(ValueError):
    def __init__(self, code: str, message: str):
        self.error_code = code
        super().__init__(message)


def _now() -> datetime:
    return datetime.now(timezone.utc)


# ── Risk 层级（R0-R4，对齐既有 Execution Policy；禁 L0-L4 Autonomy）────────
RISK_LEVELS = ["R0", "R1", "R2", "R3", "R4"]
_RISK_ORDER = {r: i for i, r in enumerate(RISK_LEVELS)}


@dataclass
class StructuredOpsAction:
    """P11.1 — 结构化动作（immutable identity + 完整字段）。"""

    action_id: str
    version: int
    action_hash: str                      # SHA256(action_id+version+target+effect+params)
    action_type: str                      # patch_resource / restart / scale / restricted_shell / runbook / rollback
    run_id: str
    tenant_id: str
    cluster_id: str
    resource_id: str
    namespace: str
    parameters: dict[str, Any]
    idempotency_key: str
    resource_version: str
    expected_effect: str
    verification_policy: str              # SLI threshold / health check ref
    risk: str = "R0"
    planner_selectable: bool = True
    automatic: bool = False
    scope_kind: str = "single_cluster"

    def identity(self) -> str:
        """immutable action identity（P11.4 绑定）。"""
        return f"{self.action_id}:{self.version}"


def action_hash_of(*, action_id: str, version: int, action_type: str, resource_id: str,
                   parameters: dict[str, Any], expected_effect: str) -> str:
    raw = json.dumps({
        "action_id": action_id, "version": version, "action_type": action_type,
        "resource_id": resource_id, "parameters": parameters, "expected_effect": expected_effect,
    }, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


class OpsActionFactory:
    """P11.1 — 只有 Hypothesis/RCA 达到允许条件才生成 Structured OpsAction。"""

    def __init__(self) -> None:
        self._actions: dict[str, StructuredOpsAction] = {}

    def create(
        self, *, run_id: str, tenant_id: str, cluster_id: str, resource_id: str,
        namespace: str, action_type: str, parameters: dict[str, Any],
        expected_effect: str, verification_policy: str, risk: str,
        root_cause_confidence: float, resource_version: str,
        rca_status: str,
    ) -> StructuredOpsAction:
        """P11.1 生成条件：RCA 须 completed/confirmed；risk 合法；action_type 合法。"""
        if rca_status not in ("completed", "confirmed"):
            raise Phase11Error("RCA_NOT_READY", f"RCA 未就绪（{rca_status}），不能形成 OpsAction")
        if risk not in RISK_LEVELS:
            raise Phase11Error("ILLEGAL_RISK", f"非法 risk: {risk}")
        if action_type not in self._allowed_types():
            raise Phase11Error("ILLEGAL_ACTION_TYPE", f"非法 action_type: {action_type}")
        if action_type == "restricted_shell":
            # P11.7 restricted_shell 仍 R4、planner_selectable=false、automatic=false
            risk = "R4"
        aid = str(uuid.uuid4())
        a = StructuredOpsAction(
            action_id=aid, version=1,
            action_hash=action_hash_of(action_id=aid, version=1, action_type=action_type,
                                       resource_id=resource_id, parameters=parameters,
                                       expected_effect=expected_effect),
            action_type=action_type, run_id=run_id, tenant_id=tenant_id, cluster_id=cluster_id,
            resource_id=resource_id, namespace=namespace, parameters=dict(parameters),
            idempotency_key=f"{run_id}:{resource_id}:{action_type}",
            resource_version=resource_version, expected_effect=expected_effect,
            verification_policy=verification_policy, risk=risk,
            planner_selectable=(action_type != "restricted_shell"),
            automatic=False,
        )
        self._actions[aid] = a
        return a

    def _allowed_types(self) -> set:
        return {"patch_resource", "restart", "scale", "restricted_shell", "runbook", "rollback"}

    def get(self, action_id: str) -> StructuredOpsAction | None:
        return self._actions.get(action_id)


class AuthoritativeRiskEngine:
    """P11.2 — query-api authoritative_risk 重算，永远不低于 baseline（不新增 L0-L4 Autonomy）。"""

    def __init__(self, baseline: str = "R0") -> None:
        self._baseline = baseline

    def compute(self, *, action: StructuredOpsAction, rca_confidence: float,
                blast_radius: str, environment: str, llm_risk_suggestion: str = "R0") -> str:
        """基于 action/parameter/confidence/blast radius/environment 计算，不低于 baseline。"""
        risk = _RISK_ORDER[self._baseline]
        # RCA confidence 低 → 升 risk
        if rca_confidence < 0.6:
            risk = max(risk, _RISK_ORDER["R2"])
        if rca_confidence < 0.4:
            risk = max(risk, _RISK_ORDER["R3"])
        # blast radius
        if blast_radius in ("cluster_wide", "namespace_wide"):
            risk = max(risk, _RISK_ORDER["R3"])
        if action.action_type == "restricted_shell":
            risk = max(risk, _RISK_ORDER["R4"])
        if environment == "production":
            risk = max(risk, _RISK_ORDER["R2"])
        # LLM risk 只作建议，query-api 决定（可忽略 LLM 建议）
        # 返回不低于 baseline 的 risk
        return RISK_LEVELS[risk]


@dataclass
class Confirmation:
    """P11.3 — R2 requester 显式确认，绑定 action 关键字段。"""

    confirmation_id: str
    action_identity: str
    action_hash: str
    version: int
    target: str
    risk: str
    resource_version: str
    requester: str
    confirmed_at: str
    valid: bool = True


class ConfirmationService:
    def __init__(self) -> None:
        self._confirmations: dict[str, Confirmation] = {}

    def confirm(self, *, requester: str, action: StructuredOpsAction) -> Confirmation:
        c = Confirmation(
            confirmation_id=str(uuid.uuid4()),
            action_identity=action.identity(),
            action_hash=action.action_hash, version=action.version,
            target=f"{action.cluster_id}/{action.namespace}/{action.resource_id}",
            risk=action.risk, resource_version=action.resource_version,
            requester=requester, confirmed_at=_now().isoformat(),
        )
        self._confirmations[c.confirmation_id] = c
        return c

    def verify_binding(self, cid: str, action: StructuredOpsAction) -> None:
        """P11.3 确认绑定 action hash/version/target/risk/resourceVersion；任一修改需重确认。"""
        c = self._confirmations.get(cid)
        if c is None:
            raise Phase11Error("CONFIRMATION_NOT_FOUND", f"确认不存在: {cid}")
        if not c.valid:
            raise Phase11Error("CONFIRMATION_INVALID", "确认已失效（action 变更需重新确认）")
        target = f"{action.cluster_id}/{action.namespace}/{action.resource_id}"
        if (c.action_hash != action.action_hash or c.version != action.version
                or c.target != target or c.risk != action.risk
                or c.resource_version != action.resource_version):
            raise Phase11Error(
                "ACTION_MUTATED_NEEDS_RE_CONFIRM",
                "action 字段已变更，需重新确认（P11.3）",
            )

    def invalidate(self, cid: str) -> None:
        if cid in self._confirmations:
            self._confirmations[cid].valid = False


@dataclass
class ApprovalRecord:
    approval_id: str
    action_identity: str
    action_hash: str
    requester: str
    approver: str
    approved_at: str
    valid: bool = True


class ApprovalService:
    """P11.4 — 独立 approver；requester!=approver（admin 也不例外）；绑定 immutable action identity；
    cross-cluster approval 拒绝。

    P20 Plan1 T5（接 query-api 权威 SoT）：可选注入 AuthorizationSoTProvider。
    当配置 SoT provider 时，approve 前校验该 cluster 的权威授权（enabled + approver/requester
    capability）；SoT 不可达（异常）或 cluster 未启用 → fail-closed 拒绝（APPROVAL_SOT_UNAVAILABLE），
    不降级到本地宽松放行。未配置 provider（In-memory MVP）→ 保持既有行为。
    """

    def __init__(self, sot_provider: Any = None) -> None:
        self._approvals: dict[str, ApprovalRecord] = {}
        self._sot = sot_provider

    def approve(self, *, approver: str, requester: str, action: StructuredOpsAction,
                requester_cluster: str, approver_cluster: str) -> ApprovalRecord:
        if requester == approver:
            raise Phase11Error("SELF_APPROVAL", f"requester==approver 拒绝（{approver} 不能自批，admin 也不例外）")
        if requester_cluster != approver_cluster:
            raise Phase11Error("CROSS_CLUSTER_APPROVAL", "跨 cluster approval 拒绝")
        # P20 T5：权威 SoT 校验（fail-closed；配置了 provider 才启用）
        if self._sot is not None:
            self._verify_authority_via_sot(approver, requester, approver_cluster)
        rec = ApprovalRecord(
            approval_id=str(uuid.uuid4()),
            action_identity=action.identity(),
            action_hash=action.action_hash,
            requester=requester, approver=approver, approved_at=_now().isoformat(),
        )
        self._approvals[rec.approval_id] = rec
        return rec

    def _verify_authority_via_sot(self, approver: str, requester: str, cluster_id: str) -> None:
        """从权威 SoT 校验 approver 有 approve 权限、requester 有 confirm 权限；不可达/未启用 fail-closed。"""
        try:
            authz = self._sot.load_authorization(cluster_id)
        except Exception as exc:  # noqa: BLE001 — SoT 不可达 → fail-closed，不降级
            raise Phase11Error("APPROVAL_SOT_UNAVAILABLE", f"授权 SoT 不可达，拒绝审批: {exc}") from exc
        if not authz.get("enabled", False):
            raise Phase11Error("APPROVAL_SOT_UNAVAILABLE", f"cluster {cluster_id} 未启用授权，拒绝审批")
        capabilities = set(authz.get("capabilities", []) or [])
        if "action.approve" not in capabilities:
            raise Phase11Error("APPROVAL_SOT_UNAVAILABLE", f"approver {approver} 无 action.approve 能力")
        if "action.confirm" not in capabilities:
            raise Phase11Error("APPROVAL_SOT_UNAVAILABLE", f"requester {requester} 无 action.confirm 能力")

    def verify(self, aid: str, action: StructuredOpsAction) -> None:
        rec = self._approvals.get(aid)
        if rec is None:
            raise Phase11Error("APPROVAL_NOT_FOUND", f"approval 不存在: {aid}")
        if not rec.valid:
            raise Phase11Error("APPROVAL_INVALID", "approval 已失效")
        if rec.action_identity != action.identity() or rec.action_hash != action.action_hash:
            raise Phase11Error("APPROVAL_ACTION_MISMATCH", "approval 绑定的 action identity 已变更（需重新批准）")


class Precheck:
    """P11.5 — 执行前重新获取 current authorization/target identity/resourceVersion/current health/
    conflicting action/maintenance；任一不满足不执行。"""

    def __init__(self) -> None:
        self._snapshot: dict[str, Any] = {}

    def set_snapshot(self, **kw: Any) -> None:
        self._snapshot.update(kw)

    def verify(self, *, action: StructuredOpsAction, current_resource_version: str,
               current_health: str, conflicting_action: bool = False,
               maintenance: bool = False, authorized: bool = True) -> None:
        if not authorized:
            raise Phase11Error("PRECHECK_AUTHZ", "执行前重新授权失败")
        if conflicting_action:
            raise Phase11Error("PRECHECK_CONFLICT", "存在 conflicting action，不执行")
        if maintenance:
            raise Phase11Error("PRECHECK_MAINTENANCE", "维护窗口内，不执行")
        if current_resource_version != action.resource_version:
            raise Phase11Error("PRECHECK_RESOURCE_VERSION", "resourceVersion 已漂移，不执行")
        if current_health not in ("healthy", "degraded"):
            raise Phase11Error("PRECHECK_HEALTH", f"当前健康状态不允许执行: {current_health}")


class ExecutionAdapter:
    """P11.6/P11.7 — 只接受 structured action；patch 只允许明确资源/字段；restricted_shell 禁 fallback。

    In-memory：不连真实 K8s；模拟 before/after state。
    """

    def __init__(self) -> None:
        self._allowed_patch_fields: dict[str, set] = {}
        self._executed: list[str] = []

    def register_patch_fields(self, resource_type: str, fields: set) -> None:
        self._allowed_patch_fields[resource_type] = set(fields)

    def execute(self, *, action: StructuredOpsAction, resource_type: str,
                before_state: dict[str, Any]) -> dict[str, Any]:
        # P11.7 restricted_shell 不得作为 action failure fallback（优先于 allowed 检查）
        if action.action_type == "restricted_shell":
            raise Phase11Error("ADAPTER_NO_SHELL", "restricted_shell 不得作为执行 fallback（P11.7）")
        if action.action_type not in ("patch_resource", "restart", "scale", "runbook", "rollback"):
            raise Phase11Error("ADAPTER_REJECT", f"action_type 非结构化: {action.action_type}")
        if action.action_type == "patch_resource":
            allowed = self._allowed_patch_fields.get(resource_type, set())
            for k in action.parameters:
                if k not in allowed:
                    raise Phase11Error("ADAPTER_PATCH_FORBIDDEN", f"patch 字段未在 allowlist: {k}")
        # In-memory：模拟执行（before → after）
        after = dict(before_state)
        after.update({k: v for k, v in action.parameters.items()})
        self._executed.append(action.action_id)
        return after

    def executed_ids(self) -> list[str]:
        return list(self._executed)


class Verification:
    """P11.8 — before→execute→observe→after→compare→verdict；用 SLI/health，不用退出码。"""

    def __init__(self) -> None:
        self._verdicts: dict[str, str] = {}

    def verify(self, *, action: StructuredOpsAction, before_health: float,
               after_health: float, exit_code: int, sli_threshold: float) -> str:
        # 退出码只是 execution fact，不是 recovery verdict（P11.8）
        if exit_code != 0:
            verdict = "failed"
        elif after_health >= sli_threshold and after_health >= before_health:
            verdict = "success"
        elif after_health >= before_health:
            verdict = "partial"  # 未恶化但未达 SLI（恢复不完整）
        else:
            verdict = "regressed"  # 健康度恶化 → regressed
        self._verdicts[action.action_id] = verdict
        return verdict


class RegressionStop:
    """P11.9 — regressed 后立即停止后续自动 action；要求人工重新调查或新 Run；禁自动连续试错。"""

    def __init__(self) -> None:
        self._regressed_runs: set = set()

    def mark_regressed(self, run_id: str) -> None:
        self._regressed_runs.add(run_id)

    def assert_action_allowed(self, run_id: str) -> None:
        if run_id in self._regressed_runs:
            raise Phase11Error("REGRESSION_STOP", "run 已 regressed，停止后续自动 action，需人工重新调查或新 Run")


class RollbackService:
    """P11.10 — rollback 作为新 action（新 id/version/hash/risk/approval/execution/verification）。"""

    def __init__(self, factory: OpsActionFactory) -> None:
        self._factory = factory

    def create_rollback(self, *, original: StructuredOpsAction, before_state: dict[str, Any]) -> StructuredOpsAction:
        # 新 action（rollback 是新动作，不复用原 action_id）
        rb = self._factory.create(
            run_id=original.run_id, tenant_id=original.tenant_id, cluster_id=original.cluster_id,
            resource_id=original.resource_id, namespace=original.namespace,
            action_type="rollback",
            parameters={"restore": dict(before_state)},
            expected_effect="恢复执行前状态",
            verification_policy="health >= before_health",
            risk="R2",
            root_cause_confidence=0.8, resource_version=original.resource_version,
            rca_status="completed",
        )
        return rb
