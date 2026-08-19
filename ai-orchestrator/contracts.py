"""Versioned cross-service contracts for the Agentic AIOps control plane.

This module is intentionally not imported by the existing production routes in
Phase 1.  It is the single Python representation used to validate the shared
JSON fixtures before a later cutover.
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


class RequestContext(ContractModel):
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
        issued_at = self.issued_at
        expires_at = self.expires_at
        if issued_at.tzinfo is None or expires_at.tzinfo is None:
            raise ValueError("issued_at and expires_at must be timezone-aware")
        lifetime = expires_at.astimezone(timezone.utc) - issued_at.astimezone(timezone.utc)
        if lifetime <= timedelta(0) or lifetime > timedelta(seconds=60):
            raise ValueError("TrustedRequestContext lifetime must be between 1 and 60 seconds")
        return self


class ResourceRef(ContractModel):
    tenant_id: UUID
    cluster_id: UUID
    resource_type: str
    namespace: Optional[str] = None
    name: str
    resource_id: str

    @model_validator(mode="after")
    def validate_canonical_id(self) -> "ResourceRef":
        namespace = self.namespace or "_"
        expected = (
            f"urn:aiops:{self.tenant_id}:{self.cluster_id}:"
            f"{self.resource_type}:{namespace}:{self.name}"
        )
        if self.resource_id != expected:
            raise ValueError("resource_id must use tenant and immutable canonical cluster UUID")
        return self

    @property
    def canonical_id(self) -> str:
        return self.resource_id


class StructuredError(ContractModel):
    error_code: str
    message: str
    retryable: bool = False
    fields: Dict[str, str] = Field(default_factory=dict)

    @model_validator(mode="after")
    def validate_error_code(self) -> "StructuredError":
        if not self.error_code or not self.error_code[0].islower():
            raise ValueError("error_code must be lower snake case")
        if any(not (char.islower() or char.isdigit() or char == "_") for char in self.error_code):
            raise ValueError("error_code must be lower snake case")
        return self


class ToolDefinition(ContractModel):
    name: str
    category: str
    description: str
    read_only: bool
    risk_level: int = Field(ge=0, le=4)
    capabilities: List[str]
    available: bool
    timeout_seconds: int = Field(gt=0, le=60)


class ToolResult(ContractModel):
    tool_name: str
    success: bool
    status: ToolStatus
    summary: str
    data: Any = None
    error: Optional[StructuredError] = None
    evidence_ids: List[UUID] = Field(default_factory=list)
    started_at: datetime
    finished_at: datetime

    @model_validator(mode="after")
    def validate_result_semantics(self) -> "ToolResult":
        if self.finished_at < self.started_at:
            raise ValueError("finished_at must not precede started_at")
        if self.success and self.status not in {ToolStatus.SUCCESS, ToolStatus.PARTIAL}:
            raise ValueError("successful ToolResult must use success or partial status")
        if not self.success and self.status in {ToolStatus.SUCCESS, ToolStatus.PARTIAL}:
            raise ValueError("failed ToolResult must use a non-success status")
        if self.status == ToolStatus.PERMISSION_DENIED and not self.error:
            raise ValueError("permission_denied ToolResult requires a structured error")
        return self


class Evidence(ContractModel):
    id: UUID
    run_id: UUID
    source: str
    evidence_type: EvidenceType
    resource_id: Optional[str] = None
    cluster_id: UUID
    namespace: Optional[str] = None
    timestamp: Optional[datetime] = None
    start_time: Optional[datetime] = None
    end_time: Optional[datetime] = None
    fact: str
    confidence: float = Field(ge=0, le=1)
    severity: Optional[str] = None
    trace_id: Optional[str] = None
    raw_data: Any = None
    metadata: Dict[str, Any] = Field(default_factory=dict)


class Hypothesis(ContractModel):
    id: UUID
    title: str
    description: str
    supporting_evidence: List[UUID] = Field(default_factory=list)
    contradicting_evidence: List[UUID] = Field(default_factory=list)
    missing_evidence: List[str] = Field(default_factory=list)
    confidence: float = Field(ge=0, le=1)
    status: HypothesisStatus


class OpsAction(ContractModel):
    action_id: UUID
    run_id: UUID
    tenant_id: UUID
    cluster_id: UUID
    action_type: str
    target: ResourceRef
    parameters: Dict[str, Any] = Field(default_factory=dict)
    risk_level: int = Field(ge=0, le=4)
    expected_effect: str
    rollback: Optional[Dict[str, Any]] = None

    @model_validator(mode="after")
    def validate_target_scope(self) -> "OpsAction":
        if self.target.tenant_id != self.tenant_id or self.target.cluster_id != self.cluster_id:
            raise ValueError("OpsAction target must belong to its tenant and cluster context")
        return self


class VerificationResult(ContractModel):
    status: VerificationStatus
    before_snapshot: Dict[str, Any]
    after_snapshot: Dict[str, Any]
    observation_window_seconds: int = Field(gt=0)
    checks: List[Dict[str, Any]]
    summary: str


class OpsIntent(ContractModel):
    intent: str
    target_type: Optional[str] = None
    target_name: Optional[str] = None
    resource_id: Optional[str] = None
    cluster_id: UUID
    namespace: Optional[str] = None
    time_range: Dict[str, Any]
    requested_capabilities: List[str]
    action_mode: str


class PlanStep(ContractModel):
    id: UUID
    agent: str
    action: str
    parameters: Dict[str, Any] = Field(default_factory=dict)
    depends_on: List[UUID] = Field(default_factory=list)
    required: bool
    status: str


class InvestigationPlan(ContractModel):
    id: UUID
    goal: str
    target: Dict[str, Any]
    steps: List[PlanStep]


class SSEEvent(ContractModel):
    event: str
    run_id: UUID
    sequence: int = Field(ge=0)
    timestamp: datetime
    cluster_id: UUID
    payload: Dict[str, Any]
