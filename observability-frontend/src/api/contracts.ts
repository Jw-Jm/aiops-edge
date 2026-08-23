/**
 * Shared V9.2 contracts. UI code must render these states explicitly.
 * Mirrors ai-orchestrator/contracts.py and ai-apm-query-go/internal/contract.
 */

export type UUID = string;

export type ToolStatus =
  | "success"
  | "partial"
  | "no_data"
  | "failed"
  | "timeout"
  | "unavailable"
  | "permission_denied";

export type EvidenceType =
  | "metric_anomaly"
  | "log_pattern"
  | "log_error"
  | "trace_anomaly"
  | "k8s_state"
  | "k8s_event"
  | "alert"
  | "change"
  | "knowledge_case"
  | "topology_relation"
  | "resource_state"
  | "capacity_anomaly"
  | "hardware_event";

export type ClaimType = "fact" | "inference" | "knowledge" | "unknown";
export type HypothesisStatus = "candidate" | "supported" | "rejected" | "unknown" | "confirmed";
export type VerificationStatus = "success" | "partial" | "failed" | "regressed" | "unknown";
export type RiskLevel = "R0" | "R1" | "R2" | "R3" | "R4";
export type PlanStepStatus =
  | "pending"
  | "ready"
  | "running"
  | "success"
  | "partial"
  | "no_data"
  | "failed"
  | "timeout"
  | "unavailable"
  | "permission_denied"
  | "cancelled"
  | "skipped";
export type RunStatus =
  | "created"
  | "planning"
  | "investigating"
  | "awaiting_confirmation"
  | "awaiting_approval"
  | "executing"
  | "verifying"
  | "success"
  | "partial"
  | "failed"
  | "regressed"
  | "cancelled";
export type RunScopeKind = "single_cluster" | "multi_cluster";
export type ContextType = "run_invocation" | "run_control" | "trusted_request";

export type ToolCapability =
  | "observability.metrics.read"
  | "observability.logs.read"
  | "observability.traces.read"
  | "observability.alerts.read"
  | "observability.topology.read"
  | "kubernetes.resources.read"
  | "kubernetes.events.read"
  | "kubernetes.logs.read"
  | "changes.read"
  | "knowledge.search"
  | "control_plane.run.read"
  | "control_plane.run.write"
  | "control_plane.event.write"
  | "execution.precheck"
  | "execution.execute"
  | "execution.verify";

/** Common context claims (V9.2 §11). */
interface ContextBase {
  version: number;
  context_type: ContextType;
  issuer: string;
  audience: string;
  request_id: UUID;
  principal_type: "user" | "system";
  principal_id: UUID;
  session_id: UUID | null;
  tenant_id: UUID;
  issued_at: string;
  expires_at: string;
  nonce: UUID;
}

/** query-api → orchestrator, to create a new Run (V9.2 §11.1). */
export interface RunInvocationContext extends ContextBase {
  context_type: "run_invocation";
  source: string;
  cluster_scope: UUID[];
}

/** query-api → orchestrator, to control an existing Run (V9.2 §11.2). */
export interface RunControlContext extends ContextBase {
  context_type: "run_control";
  run_id: UUID;
  operation: "cancel" | "stream" | "action_decision";
  action_id: UUID | null;
  decision_id: UUID | null;
}

/** orchestrator → query-api, for tool/data access (V9.2 §11.3). */
export interface TrustedRequestContext extends ContextBase {
  context_type: "trusted_request";
  run_id: UUID;
  scope_kind: "cluster" | "run";
  cluster_id: UUID | null;
  capability: ToolCapability;
  source: string;
}

/** DEPRECATED legacy single-context compatibility type. Not a target contract. */
export interface RequestContext {
  version: number;
  issuer: string;
  audience: string;
  request_id: UUID;
  run_id: UUID;
  user_id: UUID;
  session_id: UUID;
  tenant_id: UUID;
  cluster_id: UUID;
  source: string;
  capability: string;
  issued_at: string;
  expires_at: string;
  nonce: UUID;
}

/** Resource identity does NOT include tenant_id (V9.2 §10). tenant_id is isolation dimension. */
export interface ResourceRef {
  cluster_id: UUID;
  resource_type: string;
  namespace: string | null;
  name: string;
  resource_id: string;
  tenant_id: UUID;
}

/** Unified V9.2 §58 error codes. Keep in sync with contracts.py ErrorCode. */
export type ErrorCode =
  | "AUTH_REQUIRED"
  | "SESSION_REVOKED"
  | "SERVICE_AUTH_FAILED"
  | "INVALID_CONTEXT"
  | "CONTEXT_EXPIRED"
  | "CONTEXT_REPLAYED"
  | "CONTEXT_SCOPE_MISMATCH"
  | "TENANT_ACCESS_DENIED"
  | "CLUSTER_ACCESS_DENIED"
  | "RESOURCE_NOT_FOUND"
  | "RESOURCE_AMBIGUOUS"
  | "CLUSTER_UNAVAILABLE"
  | "CLUSTER_IDENTITY_MISMATCH" // P3.10c-final: credential→cluster identity binding conflict (409)
  | "NO_DATA"
  | "BACKEND_UNAVAILABLE"
  | "TOOL_UNAVAILABLE"
  | "TOOL_TIMEOUT"
  | "VALIDATION_FAILED"
  | "RUN_STATE_CONFLICT"
  | "RUN_CANCELLED"
  | "ACTION_NOT_ALLOWED"
  | "ACTION_CONFIRMATION_REQUIRED"
  | "ACTION_APPROVAL_REQUIRED"
  | "APPROVAL_EXPIRED"
  | "APPROVAL_SCOPE_MISMATCH"
  | "RESOURCE_VERSION_CONFLICT"
  | "MAINTENANCE_MODE";

export interface StructuredError {
  error_code: ErrorCode;
  message: string;
  retryable: boolean;
  fields: Record<string, string>;
}

/** ToolResult — success=true is allowed with status success | partial | no_data. */
export interface ToolResult {
  tool_name: string;
  cluster_id: UUID;
  success: boolean;
  status: ToolStatus;
  summary: string;
  data: unknown;
  error_code: string | null;
  error_message: string | null;
  retryable: boolean;
  evidence_ids: UUID[];
  source_system: string;
  query_id: string | null;
  time_range: Record<string, unknown> | null;
  started_at: string;
  finished_at: string;
}

export interface Evidence {
  evidence_id: UUID;
  run_id: UUID;
  tenant_id: UUID;
  cluster_id: UUID;
  evidence_type: EvidenceType;
  claim_type: ClaimType;
  source: string;
  source_reliability: number;
  resource_id: string | null;
  namespace: string | null;
  service: string | null;
  pod: string | null;
  node: string | null;
  trace_id: string | null;
  observed_at: string | null;
  time_range_start: string | null;
  time_range_end: string | null;
  fact: string;
  raw_ref: string | null;
  raw_digest_sha256: string | null;
  metadata: Record<string, unknown>;
  provenance_fingerprint: string;
  created_at: string;
}

export interface Hypothesis {
  hypothesis_id: UUID;
  run_id: UUID;
  title: string;
  description: string;
  supporting_evidence: UUID[];
  contradicting_evidence: UUID[];
  missing_evidence: string[];
  confidence: number;
  status: HypothesisStatus;
}

export interface OpsAction {
  action_id: UUID;
  run_id: UUID;
  tenant_id: UUID;
  cluster_id: UUID;
  target_resource_id: string;
  resource_version: string;
  action_type: string;
  parameters: Record<string, unknown>;
  proposed_risk: RiskLevel;
  authoritative_risk: RiskLevel;
  expected_effect: string;
  verification_policy_id: string;
  rollback_strategy: Record<string, unknown> | null;
  action_hash: string;
  idempotency_key: string;
  created_by: UUID;
  created_at: string;
}

export interface Approval {
  approval_id: UUID;
  run_id: UUID;
  action_id: UUID;
  action_hash: string;
  tenant_id: UUID;
  cluster_id: UUID;
  target_resource_id: string;
  resource_version: string;
  risk_level: RiskLevel;
  requested_by: UUID;
  approved_by: UUID | null;
  created_at: string;
  expires_at: string;
  decision: string;
}

export interface VerificationResult {
  verification_id: UUID;
  run_id: UUID;
  action_id: UUID;
  status: VerificationStatus;
  before_snapshot: Record<string, unknown>;
  after_snapshot: Record<string, unknown>;
  observation_window_seconds: number;
  checks: Array<Record<string, unknown>>;
  summary: string;
}

export interface OpsIntent {
  intent: string;
  target_type: string | null;
  target_name: string | null;
  resource_id: string | null;
  cluster_id: UUID;
  namespace: string | null;
  time_range: Record<string, unknown>;
  requested_capabilities: ToolCapability[];
  action_mode: string;
}

export interface PlanStep {
  id: UUID;
  run_id: UUID;
  agent: string;
  action: string;
  parameters: Record<string, unknown>;
  depends_on: UUID[];
  required: boolean;
  cluster_id: UUID | null;
  status: PlanStepStatus;
}

export interface Run {
  run_id: UUID;
  request_id: UUID;
  tenant_id: UUID;
  principal_type: "user" | "system";
  principal_id: UUID;
  session_id: UUID | null;
  scope_kind: RunScopeKind;
  primary_cluster_id: UUID | null;
  intent: string;
  action_mode: string;
  target_type: string | null;
  target_resource_id: string | null;
  time_range_start: string | null;
  time_range_end: string | null;
  status: RunStatus;
  state_version: number;
  parent_run_id: UUID | null;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
}

/** cluster_id is null for multi-cluster aggregate events (V9.2 §54). */
export interface SSEEvent {
  event: string;
  run_id: UUID;
  sequence: number;
  timestamp: string;
  tenant_id: UUID;
  cluster_id: UUID | null;
  payload: Record<string, unknown>;
}
