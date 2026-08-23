"""Versioned cross-service contracts for the Agentic AIOps control plane (V9.2).

This is the single authoritative Python representation of the frozen V9.2
contracts. It is the contract mainline; no parallel v9_contracts.* is allowed.

Changes in this module are contract-freeze changes (V9.2 Phase 2) and must be
mirrored across Go (ai-apm-query-go/internal/contract) and TypeScript
(observability-frontend/src/api/contracts.ts), plus the shared fixtures.
"""

from datetime import datetime, timedelta, timezone
from enum import Enum
from typing import Any, Dict, List, Mapping, Optional, Type, TypeVar
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, ValidationError, model_validator


class ToolStatus(str, Enum):
    SUCCESS = "success"
    PARTIAL = "partial"
    NO_DATA = "no_data"
    FAILED = "failed"
    TIMEOUT = "timeout"
    UNAVAILABLE = "unavailable"
    PERMISSION_DENIED = "permission_denied"


class EvidenceType(str, Enum):
    METRIC_ANOMALY = "metric_anomaly"
    LOG_PATTERN = "log_pattern"
    LOG_ERROR = "log_error"
    TRACE_ANOMALY = "trace_anomaly"
    K8S_STATE = "k8s_state"
    K8S_EVENT = "k8s_event"
    ALERT = "alert"
    CHANGE = "change"
    KNOWLEDGE_CASE = "knowledge_case"
    TOPOLOGY_RELATION = "topology_relation"
    RESOURCE_STATE = "resource_state"
    CAPACITY_ANOMALY = "capacity_anomaly"
    HARDWARE_EVENT = "hardware_event"


class ClaimType(str, Enum):
    FACT = "fact"
    INFERENCE = "inference"
    KNOWLEDGE = "knowledge"
    UNKNOWN = "unknown"


class HypothesisStatus(str, Enum):
    CANDIDATE = "candidate"
    SUPPORTED = "supported"
    REJECTED = "rejected"
    UNKNOWN = "unknown"
    CONFIRMED = "confirmed"


class VerificationStatus(str, Enum):
    SUCCESS = "success"
    PARTIAL = "partial"
    FAILED = "failed"
    REGRESSED = "regressed"
    UNKNOWN = "unknown"


class RiskLevel(str, Enum):
    R0 = "R0"
    R1 = "R1"
    R2 = "R2"
    R3 = "R3"
    R4 = "R4"


class PlanStepStatus(str, Enum):
    PENDING = "pending"
    READY = "ready"
    RUNNING = "running"
    SUCCESS = "success"
    PARTIAL = "partial"
    NO_DATA = "no_data"
    FAILED = "failed"
    TIMEOUT = "timeout"
    UNAVAILABLE = "unavailable"
    PERMISSION_DENIED = "permission_denied"
    CANCELLED = "cancelled"
    SKIPPED = "skipped"


class RunStatus(str, Enum):
    CREATED = "created"
    PLANNING = "planning"
    INVESTIGATING = "investigating"
    AWAITING_CONFIRMATION = "awaiting_confirmation"
    AWAITING_APPROVAL = "awaiting_approval"
    EXECUTING = "executing"
    VERIFYING = "verifying"
    SUCCESS = "success"
    PARTIAL = "partial"
    FAILED = "failed"
    REGRESSED = "regressed"
    CANCELLED = "cancelled"


class RunScopeKind(str, Enum):
    SINGLE_CLUSTER = "single_cluster"
    MULTI_CLUSTER = "multi_cluster"


class ContextType(str, Enum):
    RUN_INVOCATION = "run_invocation"
    RUN_CONTROL = "run_control"
    TRUSTED_REQUEST = "trusted_request"


class ToolCapability(str, Enum):
    METRICS_READ = "observability.metrics.read"
    LOGS_READ = "observability.logs.read"
    TRACES_READ = "observability.traces.read"
    ALERTS_READ = "observability.alerts.read"
    TOPOLOGY_READ = "observability.topology.read"
    K8S_RESOURCES_READ = "kubernetes.resources.read"
    K8S_EVENTS_READ = "kubernetes.events.read"
    K8S_LOGS_READ = "kubernetes.logs.read"
    CHANGES_READ = "changes.read"
    KNOWLEDGE_SEARCH = "knowledge.search"
    CONTROL_PLANE_RUN_READ = "control_plane.run.read"
    CONTROL_PLANE_RUN_WRITE = "control_plane.run.write"
    CONTROL_PLANE_EVENT_WRITE = "control_plane.event.write"
    EXECUTION_PRECHECK = "execution.precheck"
    EXECUTION_EXECUTE = "execution.execute"
    EXECUTION_VERIFY = "execution.verify"


# V9.2 §58 unified error codes.
class ErrorCode(str, Enum):
    AUTH_REQUIRED = "AUTH_REQUIRED"
    SESSION_REVOKED = "SESSION_REVOKED"
    SERVICE_AUTH_FAILED = "SERVICE_AUTH_FAILED"
    INVALID_CONTEXT = "INVALID_CONTEXT"
    CONTEXT_EXPIRED = "CONTEXT_EXPIRED"
    CONTEXT_REPLAYED = "CONTEXT_REPLAYED"
    CONTEXT_SCOPE_MISMATCH = "CONTEXT_SCOPE_MISMATCH"
    TENANT_ACCESS_DENIED = "TENANT_ACCESS_DENIED"
    CLUSTER_ACCESS_DENIED = "CLUSTER_ACCESS_DENIED"
    RESOURCE_NOT_FOUND = "RESOURCE_NOT_FOUND"
    RESOURCE_AMBIGUOUS = "RESOURCE_AMBIGUOUS"
    CLUSTER_UNAVAILABLE = "CLUSTER_UNAVAILABLE"
    # V9.2 P3.10c-final: the credential resolved for a canonical cluster reached a
    # Kubernetes API whose observed identity differs from the identity bound to the
    # registration. This is a binding conflict (409), not a backend outage.
    CLUSTER_IDENTITY_MISMATCH = "CLUSTER_IDENTITY_MISMATCH"
    NO_DATA = "NO_DATA"
    BACKEND_UNAVAILABLE = "BACKEND_UNAVAILABLE"
    TOOL_UNAVAILABLE = "TOOL_UNAVAILABLE"
    TOOL_TIMEOUT = "TOOL_TIMEOUT"
    VALIDATION_FAILED = "VALIDATION_FAILED"
    RUN_STATE_CONFLICT = "RUN_STATE_CONFLICT"
    RUN_CANCELLED = "RUN_CANCELLED"
    ACTION_NOT_ALLOWED = "ACTION_NOT_ALLOWED"
    ACTION_CONFIRMATION_REQUIRED = "ACTION_CONFIRMATION_REQUIRED"
    ACTION_APPROVAL_REQUIRED = "ACTION_APPROVAL_REQUIRED"
    APPROVAL_EXPIRED = "APPROVAL_EXPIRED"
    APPROVAL_SCOPE_MISMATCH = "APPROVAL_SCOPE_MISMATCH"
    RESOURCE_VERSION_CONFLICT = "RESOURCE_VERSION_CONFLICT"
    MAINTENANCE_MODE = "MAINTENANCE_MODE"


class ContractModel(BaseModel):
    model_config = ConfigDict(extra="forbid", str_strip_whitespace=True)


class ContractValidationError(ValueError):
    """Stable public validation error without framework-specific stack details."""

    error_code = "contract_validation_error"

    def __init__(self, fields: Dict[str, Dict[str, str]]):
        self.fields = fields
        super().__init__(self.error_code)


ModelT = TypeVar("ModelT", bound=ContractModel)


def validate_contract(model_type: Type[ModelT], payload: Mapping[str, Any]) -> ModelT:
    """Validate a contract and expose stable field-path errors to API adapters."""

    try:
        return model_type.model_validate(payload)
    except ValidationError as exc:
        fields: Dict[str, Dict[str, str]] = {}
        for item in exc.errors():
            path = ".".join(str(part) for part in item["loc"]) or "__root__"
            fields[path] = {"path": path, "message": str(item["msg"])}
        raise ContractValidationError(fields) from None


# ═══════════════════════════════════════════════════════════════════════
# V9.2 §11 — Three internal contexts
# ═══════════════════════════════════════════════════════════════════════


class _ContextBase(ContractModel):
    version: int = Field(default=1, ge=1)
    context_type: ContextType
    issuer: str
    audience: str
    request_id: UUID
    principal_type: str
    principal_id: UUID
    session_id: Optional[UUID] = None
    tenant_id: UUID
    issued_at: datetime
    expires_at: datetime
    nonce: UUID

    @model_validator(mode="after")
    def validate_common(self) -> "_ContextBase":
        if self.principal_type not in {"user", "system"}:
            raise ValueError("principal_type must be user or system")
        if self.principal_type == "user" and self.session_id is None:
            raise ValueError("user principal requires session_id")
        if self.principal_type == "system" and self.session_id is not None:
            raise ValueError("system principal must have null session_id")
        if self.issued_at.tzinfo is None or self.expires_at.tzinfo is None:
            raise ValueError("issued_at and expires_at must be timezone-aware")
        lifetime = self.expires_at.astimezone(timezone.utc) - self.issued_at.astimezone(timezone.utc)
        if lifetime <= timedelta(0) or lifetime > timedelta(seconds=60):
            raise ValueError("context lifetime must be between 1 and 60 seconds")
        return self


class RunInvocationContext(_ContextBase):
    """query-api → orchestrator, to create a new Run (V9.2 §11.1)."""

    context_type: ContextType = ContextType.RUN_INVOCATION
    source: str
    cluster_scope: List[UUID] = Field(min_length=1)

    @model_validator(mode="after")
    def validate_scope(self) -> "RunInvocationContext":
        # cluster_scope is the target scope for this run, not an authorization list.
        return self


class RunControlContext(_ContextBase):
    """query-api → orchestrator, to control an existing Run (V9.2 §11.2)."""

    context_type: ContextType = ContextType.RUN_CONTROL
    run_id: UUID
    operation: str
    action_id: Optional[UUID] = None
    decision_id: Optional[UUID] = None

    @model_validator(mode="after")
    def validate_operation(self) -> "RunControlContext":
        if self.operation not in {"cancel", "stream", "action_decision"}:
            raise ValueError("operation must be cancel, stream, or action_decision")
        return self


class TrustedRequestContext(_ContextBase):
    """orchestrator → query-api, for tool/data access (V9.2 §11.3)."""

    context_type: ContextType = ContextType.TRUSTED_REQUEST
    run_id: UUID
    scope_kind: str
    cluster_id: Optional[UUID] = None
    capability: ToolCapability
    source: str

    @model_validator(mode="after")
    def validate_scope_kind(self) -> "TrustedRequestContext":
        if self.scope_kind not in {"cluster", "run"}:
            raise ValueError("scope_kind must be cluster or run")
        if self.scope_kind == "cluster" and self.cluster_id is None:
            raise ValueError("cluster scope requires cluster_id")
        if self.scope_kind == "run":
            if self.cluster_id is not None:
                raise ValueError("run scope must have null cluster_id")
            if not str(self.capability.value).startswith("control_plane."):
                raise ValueError("run scope only allows control_plane.* capability")
        return self


# Deprecated legacy single context — target contract is the three contexts above.
# Kept temporarily in Phase 2 only for production callers that still depend on it.
# NOT a target contract; must not gain new callers; removed after Phase 3 callers switch.
class RequestContext(ContractModel):
    """DEPRECATED: legacy single-context compatibility type. Use the three V9.2 contexts."""

    version: int = Field(default=1, ge=1)
    issuer: str
    audience: str
    request_id: UUID
    run_id: UUID
    user_id: UUID
    session_id: UUID
    tenant_id: UUID
    cluster_id: UUID
    source: str
    capability: str
    issued_at: datetime
    expires_at: datetime
    nonce: UUID

    @model_validator(mode="after")
    def validate_lifetime(self) -> "RequestContext":
        if self.issued_at.tzinfo is None or self.expires_at.tzinfo is None:
            raise ValueError("issued_at and expires_at must be timezone-aware")
        lifetime = self.expires_at.astimezone(timezone.utc) - self.issued_at.astimezone(timezone.utc)
        if lifetime <= timedelta(0) or lifetime > timedelta(seconds=60):
            raise ValueError("TrustedRequestContext lifetime must be between 1 and 60 seconds")
        return self


# ═══════════════════════════════════════════════════════════════════════
# Resource Identity (V9.2 §10) — canonical resource_id does NOT include tenant
# ═══════════════════════════════════════════════════════════════════════


class ResourceRef(ContractModel):
    cluster_id: UUID
    resource_type: str
    namespace: Optional[str] = None
    name: str
    resource_id: str
    # tenant_id is an authorization / data-isolation dimension, not part of identity.
    tenant_id: UUID = Field(description="ownership/isolation dimension; not part of resource_id")

    @model_validator(mode="after")
    def validate_canonical_id(self) -> "ResourceRef":
        namespace = self.namespace or "_"
        expected = f"{self.resource_type}:{self.cluster_id}:{namespace}:{self.name}"
        if self.resource_id != expected:
            raise ValueError(
                "resource_id must be <type>:<cluster_uuid>:<namespace-or->_>:<name> "
                "and must NOT include tenant_id"
            )
        return self

    @property
    def canonical_id(self) -> str:
        return self.resource_id


# ═══════════════════════════════════════════════════════════════════════
# Tool (V9.2 §30-32)
# ═══════════════════════════════════════════════════════════════════════


class ToolDefinition(ContractModel):
    name: str
    category: str
    description: str
    read_only: bool
    risk_level: int = Field(ge=0, le=4)
    capability: ToolCapability
    availability: str  # available / unavailable / unknown
    input_schema: Dict[str, Any] = Field(default_factory=dict)
    output_schema: Dict[str, Any] = Field(default_factory=dict)
    timeout_class: str  # default / long_query
    planner_selectable: bool = True
    automatic: bool = False


class ToolResult(ContractModel):
    tool_name: str
    cluster_id: UUID
    success: bool
    status: ToolStatus
    summary: str
    data: Any = None
    error_code: Optional[str] = None
    error_message: Optional[str] = None
    retryable: bool = False
    evidence_ids: List[UUID] = Field(default_factory=list)
    source_system: str
    query_id: Optional[str] = None
    time_range: Optional[Dict[str, Any]] = None
    started_at: datetime
    finished_at: datetime
    # ── V1 冻结 ToolResult 到此为止（15 字段，对齐 Python/TS binding + 共享 fixture；
    #    Go binding 另有内部 Error *StructuredError，经 json:"-" 不入 wire）──
    # 平行 tool_result.py 的 tenant_id/tool_id/request_id/retry_policy/evidence_required/
    # duration_ms/provenance/partial_reason/denied_scope 等 V2 演进字段移入独立草案 Schema
    # contracts_v2_draft.py，不得混入 V1 类（违反 V1 wire 冻结）。

    @model_validator(mode="after")
    def validate_result_semantics(self) -> "ToolResult":
        if self.finished_at < self.started_at:
            raise ValueError("finished_at must not precede started_at")
        # V9.2: "executed successfully" and "has data" are distinct.
        # success=true is allowed with status in {success, partial, no_data}.
        if self.success and self.status not in {
            ToolStatus.SUCCESS, ToolStatus.PARTIAL, ToolStatus.NO_DATA,
        }:
            raise ValueError("successful ToolResult must use success, partial, or no_data")
        if not self.success and self.status in {ToolStatus.SUCCESS, ToolStatus.PARTIAL, ToolStatus.NO_DATA}:
            raise ValueError("failed ToolResult must use a non-success status")
        if self.status == ToolStatus.PERMISSION_DENIED and not self.error_code:
            raise ValueError("permission_denied ToolResult requires a structured error code")
        return self


# ═══════════════════════════════════════════════════════════════════════
# Evidence (V9.2 §33-36)
# ═══════════════════════════════════════════════════════════════════════


class Evidence(ContractModel):
    evidence_id: UUID
    run_id: UUID
    tenant_id: UUID
    cluster_id: UUID
    evidence_type: EvidenceType
    claim_type: ClaimType
    source: str
    source_reliability: float = Field(ge=0, le=1)
    resource_id: Optional[str] = None
    namespace: Optional[str] = None
    service: Optional[str] = None
    pod: Optional[str] = None
    node: Optional[str] = None
    trace_id: Optional[str] = None
    observed_at: Optional[datetime] = None
    time_range_start: Optional[datetime] = None
    time_range_end: Optional[datetime] = None
    fact: str
    raw_ref: Optional[str] = None
    raw_digest_sha256: Optional[str] = None
    metadata: Dict[str, Any] = Field(default_factory=dict)
    provenance_fingerprint: str
    created_at: datetime

    @model_validator(mode="after")
    def validate_claim(self) -> "Evidence":
        if self.claim_type == ClaimType.FACT and not self.resource_id and not self.raw_digest_sha256:
            raise ValueError("fact evidence must reference on-scene data (resource_id or raw_digest)")
        if self.claim_type == ClaimType.INFERENCE and not self.metadata.get("supporting_evidence_ids"):
            raise ValueError("inference evidence must reference supporting evidence IDs")
        if self.claim_type == ClaimType.KNOWLEDGE and not self.metadata.get("source_ref"):
            raise ValueError("knowledge evidence must reference document/source")
        if self.claim_type == ClaimType.UNKNOWN and not self.metadata.get("reason"):
            raise ValueError("unknown evidence must record missing evidence/capability/permission/availability reason")
        return self


# ═══════════════════════════════════════════════════════════════════════
# Hypothesis (V9.2 §40)
# ═══════════════════════════════════════════════════════════════════════


class Hypothesis(ContractModel):
    hypothesis_id: UUID
    run_id: UUID
    title: str
    description: str
    supporting_evidence: List[UUID] = Field(default_factory=list)
    contradicting_evidence: List[UUID] = Field(default_factory=list)
    missing_evidence: List[str] = Field(default_factory=list)
    confidence: float = Field(ge=0, le=1)
    status: HypothesisStatus
    # R2 强隔离（V2 演进，带默认向后兼容）：一个 Hypothesis 不得混用跨 tenant/cluster/resource 证据
    tenant_id: Optional[UUID] = None
    cluster_id: Optional[UUID] = None
    resource_id: str = ""
    affected_resource: str = ""


# ═══════════════════════════════════════════════════════════════════════
# R2 Task4 — Hypothesis/RcaResult 强隔离 + 类型定义
# ═══════════════════════════════════════════════════════════════════════


class HypothesisScore(ContractModel):
    """评分分量（可复现，禁 LLM 给 confidence 数字）。"""
    llm_prior: float = Field(ge=0, le=1)
    evidence_support: float = Field(ge=0, le=1)
    source_reliability: float = Field(ge=0, le=1)
    temporal: float = Field(ge=0, le=1)
    contradiction_penalty: float = Field(le=0)
    missing_penalty: float = Field(le=0)
    final_score: float = Field(ge=0, le=1)
    # R2 收敛 v0.3（V2 演进，带默认）：关联 hypothesis（供反查/复算）
    hypothesis_id: Optional[UUID] = None


class Contradiction(ContractModel):
    """矛盾判定。"""
    kind: str  # time_conflict / resource_cluster_conflict / metric_log_trace_conflict / change_after_fault / temporal_relation_weak
    severity: str  # critical / normal
    detail: str = ""
    resolved: bool = False
    # R2 收敛 v0.3（V2 演进，带默认）：关联 hypothesis/evidence（供反查/复算，杜绝有损映射）
    hypothesis_id: Optional[UUID] = None
    evidence_id: Optional[UUID] = None


class MissingEvidence(ContractModel):
    """缺失证据。"""
    kind: str  # critical / optional
    reason: str
    description: str = ""
    # R2 收敛 v0.3（V2 演进，带默认）：关联 hypothesis + 原始类型 + follow-up slot（杜绝把 required_type 塞进 description）
    hypothesis_id: Optional[UUID] = None
    required_type: str = ""
    followup_slot: Optional[str] = None


class RootCauseRef(ContractModel):
    """根因候选引用（指向已登记 Evidence + Hypothesis）。"""
    hypothesis_id: UUID
    evidence_ids: List[UUID] = Field(default_factory=list)
    final_score: float = Field(ge=0, le=1)


class RcaResult(ContractModel):
    """Phase9 RCA 产出（Root Cause/Unknowns，不触发执行）。

    强隔离：必须含 tenant/cluster/run/resource 隔离维度；
    root_cause 为 None 时（unknown）automatic_remediation 必须 False。
    """
    rca_id: UUID
    run_id: UUID
    tenant_id: UUID
    cluster_id: UUID
    resource_id: str
    root_cause: Optional[str] = None
    confidence: float = Field(ge=0, le=1)
    status: str = "completed"  # completed / unknown
    conclusion_state: str = "unknown"  # R2 收敛 v0.3（V2 演进，带默认）：confirmed/supported/rejected/unknown（权威状态）
    hypothesis_scores: List[HypothesisScore] = Field(default_factory=list)
    contradictions: List[Contradiction] = Field(default_factory=list)
    missing_evidence: List[MissingEvidence] = Field(default_factory=list)
    root_cause_refs: List[RootCauseRef] = Field(default_factory=list)
    automatic_remediation: bool = False
    ops_actions: List[str] = Field(default_factory=list)

    @model_validator(mode="after")
    def unknown_safe(self) -> "RcaResult":
        if self.root_cause is None and self.automatic_remediation:
            raise ValueError("unknown root_cause 不得 automatic_remediation（Unknown-safe）")
        return self

    @model_validator(mode="after")
    def state_matrix(self) -> "RcaResult":
        """R2 收敛 v0.3 §5：conclusion_state × root_cause/confidence/root_cause_refs 合法组合。

        仅当 conclusion_state 被显式设定为 confirmed/supported/rejected 时严格校验；
        默认/unknown 不强制 root_cause is None（向后兼容：既有构造可 root_cause 非空而
        未显式设 conclusion_state，unknown_safe 仍兜底）。
        - confirmed/supported：必须 root_cause 非空 + root_cause_refs 非空；confirmed 需
          confidence>=0.80，supported 需 0.60<=confidence<0.80。
        - rejected：必须 root_cause 为空（None）。
        - 违规 → fail-closed ValueError。
        """
        s = self.conclusion_state
        if s in ("confirmed", "supported"):
            if self.root_cause is None:
                raise ValueError(f"conclusion_state={s} 必须有 root_cause（非 None）")
            if not self.root_cause_refs:
                raise ValueError(f"conclusion_state={s} 必须有 root_cause_refs")
            if s == "confirmed" and self.confidence < 0.80:
                raise ValueError("confirmed 需 confidence>=0.80")
            if s == "supported" and not (0.60 <= self.confidence < 0.80):
                raise ValueError("supported 需 0.60<=confidence<0.80")
        elif s == "rejected" and self.root_cause is not None:
            raise ValueError("conclusion_state=rejected 必须 root_cause is None")
        return self


# ═══════════════════════════════════════════════════════════════════════
# OpsAction (V9.2 §45-46)
# ═══════════════════════════════════════════════════════════════════════


class OpsAction(ContractModel):
    action_id: UUID
    run_id: UUID
    tenant_id: UUID
    cluster_id: UUID
    target_resource_id: str
    resource_version: str
    action_type: str  # restart_workload / scale_workload / rollback_deployment / patch_resource / restricted_shell
    parameters: Dict[str, Any] = Field(default_factory=dict)
    proposed_risk: RiskLevel
    authoritative_risk: RiskLevel
    expected_effect: str
    verification_policy_id: str
    rollback_strategy: Optional[Dict[str, Any]] = None
    action_hash: str
    idempotency_key: str
    created_by: UUID
    created_at: datetime

    @model_validator(mode="after")
    def validate_risk(self) -> "OpsAction":
        # Authoritative risk can never be lower than proposed risk.
        if self.authoritative_risk.value < self.proposed_risk.value:
            raise ValueError("authoritative_risk must be same or higher than proposed_risk")
        return self


class Approval(ContractModel):
    approval_id: UUID
    run_id: UUID
    action_id: UUID
    action_hash: str
    tenant_id: UUID
    cluster_id: UUID
    target_resource_id: str
    resource_version: str
    risk_level: RiskLevel
    requested_by: UUID
    approved_by: Optional[UUID] = None
    created_at: datetime
    expires_at: datetime
    decision: str  # pending / approved / rejected / expired


class VerificationPolicy(ContractModel):
    policy_id: str
    action_type: str
    success_criteria: List[Dict[str, Any]] = Field(default_factory=list)
    observation_window_seconds: int = Field(gt=0)
    regression_conditions: List[Dict[str, Any]] = Field(default_factory=list)


class VerificationResult(ContractModel):
    verification_id: UUID
    run_id: UUID
    action_id: UUID
    status: VerificationStatus
    before_snapshot: Dict[str, Any]
    after_snapshot: Dict[str, Any]
    observation_window_seconds: int = Field(gt=0)
    checks: List[Dict[str, Any]]
    summary: str


# ═══════════════════════════════════════════════════════════════════════
# Intent / Plan (V9.2 §28.1-28.3)
# ═══════════════════════════════════════════════════════════════════════


class OpsIntent(ContractModel):
    intent: str  # query / health_check / diagnose / root_cause_analysis / knowledge_search / capacity_analysis / remediation / execute / verify
    target_type: Optional[str] = None
    target_name: Optional[str] = None
    resource_id: Optional[str] = None
    cluster_id: UUID
    namespace: Optional[str] = None
    time_range: Dict[str, Any]
    requested_capabilities: List[ToolCapability]
    action_mode: str  # read_only / plan_only / execute_allowed


class PlanStep(ContractModel):
    id: UUID
    run_id: UUID
    agent: str
    action: str
    parameters: Dict[str, Any] = Field(default_factory=dict)
    depends_on: List[UUID] = Field(default_factory=list)
    required: bool
    cluster_id: Optional[UUID] = None  # nullable: aggregate step vs tool-exec step
    status: PlanStepStatus


class InvestigationPlan(ContractModel):
    plan_id: UUID
    run_id: UUID
    goal: str
    target: Dict[str, Any]
    steps: List[PlanStep]


# ═══════════════════════════════════════════════════════════════════════
# R2 Task2 — EvidenceLifecycleState（Evidence 不可变 + 生命周期外部化，§三十四）
# ═══════════════════════════════════════════════════════════════════════


class EvidenceLifecycleState(str, Enum):
    CREATED = "created"
    VALIDATED = "validated"
    EXPIRED = "expired"
    ARCHIVED = "archived"


class EvidenceState(ContractModel):
    """按 evidence_id 管理 Evidence 的生命周期状态（不污染不可变 Evidence 本体）。"""
    evidence_id: UUID
    status: EvidenceLifecycleState
    reference_status: str = Field(default="current")  # current|stale
    version: int = Field(default=1, ge=1)
    updated_at: datetime
    transitioned_from: Optional[str] = None


# ═══════════════════════════════════════════════════════════════════════
# R2 Task3 — PlannerState（Planner 唯一状态，预算固化）
# ═══════════════════════════════════════════════════════════════════════


class PlannerBudget(ContractModel):
    """Planner 预算（唯一预算源，防副调查图/预算透支）。"""
    max_steps: int = Field(default=20, ge=1)
    max_followup_rounds: int = Field(default=2, ge=0)
    consumed_steps: int = Field(default=0, ge=0)
    consumed_followup_rounds: int = Field(default=0, ge=0)
    consumed_evidence_queries: int = Field(default=0, ge=0)


class PlanStepRuntimeState(ContractModel):
    """Planner 步骤运行状态（Y2：唯一运行状态源，独立于定义态 InvestigationPlan）。

    不变量：
    - 未完成（lifecycle != completed）→ outcome 必须为空。
    - 已完成（lifecycle == completed）→ outcome 必填（success/failed/skipped）。
    - 时间必须带时区（naive 拒绝，防跨环境漂移）。
    - attempt ≥ 0。
    lifecycle（completed=生命周期结束）与 outcome（执行结果）语义分离，不混叠。
    """
    step_id: UUID
    result_ref: Optional[str] = None
    started_at: Optional[datetime] = None
    finished_at: Optional[datetime] = None
    attempt: int = Field(default=0, ge=0)
    outcome: Optional[str] = None  # success/failed/skipped
    lifecycle: str = "pending"  # pending/running/completed

    @model_validator(mode="after")
    def validate_state(self) -> "PlanStepRuntimeState":
        if self.lifecycle != "completed" and self.outcome is not None:
            raise ValueError(f"未完成步骤不得有 outcome: {self.outcome!r}")
        if self.lifecycle == "completed" and not self.outcome:
            raise ValueError("已完成步骤必须记录 outcome")
        for t in (self.started_at, self.finished_at):
            if t is not None and t.tzinfo is None:
                raise ValueError("步骤时间必须带时区（naive 拒绝）")
        return self


class PlannerState(ContractModel):
    """Planner 唯一调查状态（预算固化 + DAG 推进 + 运行状态源）。

    权威源：PlannerState 是 Planner 内部唯一状态（非 prompt 历史），
    预算消耗/剩余必须由 Planner 计算，禁 LLM/Agent 覆盖。
    """
    run_id: UUID
    plan: InvestigationPlan
    budget: PlannerBudget
    completed_step_ids: List[UUID] = Field(default_factory=list)
    findings: List[str] = Field(default_factory=list)
    status: PlanStepStatus = PlanStepStatus.PENDING
    # Y2：步骤运行状态（唯一运行状态源）+ label 索引（随 Plan 持久化，供 UUID→label 反查）
    step_runtime: Dict[UUID, PlanStepRuntimeState] = Field(default_factory=dict)
    step_label_index: Dict[str, UUID] = Field(default_factory=dict)

    @model_validator(mode="after")
    def check_budget_consistent(self) -> "PlannerState":
        if self.budget.consumed_steps > self.budget.max_steps:
            raise ValueError(
                f"PlannerState 预算透支：consumed {self.budget.consumed_steps} > max {self.budget.max_steps}"
            )
        return self


# ═══════════════════════════════════════════════════════════════════════
# Run (V9.2 §23, §28.9)
# ═══════════════════════════════════════════════════════════════════════


class Run(ContractModel):
    run_id: UUID
    request_id: UUID
    tenant_id: UUID
    principal_type: str
    principal_id: UUID
    session_id: Optional[UUID] = None
    scope_kind: RunScopeKind
    primary_cluster_id: Optional[UUID] = None
    intent: str
    action_mode: str
    target_type: Optional[str] = None
    target_resource_id: Optional[str] = None
    time_range_start: Optional[datetime] = None
    time_range_end: Optional[datetime] = None
    status: RunStatus
    state_version: int = Field(ge=0)
    parent_run_id: Optional[UUID] = None
    created_at: datetime
    updated_at: datetime
    finished_at: Optional[datetime] = None

    @model_validator(mode="after")
    def validate_scope(self) -> "Run":
        if self.scope_kind == RunScopeKind.SINGLE_CLUSTER and self.primary_cluster_id is None:
            raise ValueError("single_cluster run requires primary_cluster_id")
        if self.scope_kind == RunScopeKind.MULTI_CLUSTER and self.primary_cluster_id is not None:
            raise ValueError("multi_cluster run must have null primary_cluster_id")
        return self


# ═══════════════════════════════════════════════════════════════════════
# SSE (V9.2 §54-56)
# ═══════════════════════════════════════════════════════════════════════


class SSEEvent(ContractModel):
    event: str
    run_id: UUID
    sequence: int = Field(ge=0)
    timestamp: datetime
    tenant_id: UUID
    cluster_id: Optional[UUID] = None  # null for multi-cluster aggregate events
    payload: Dict[str, Any]
