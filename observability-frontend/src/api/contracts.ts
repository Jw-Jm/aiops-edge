/** Shared Phase 1 contracts. UI code must render these states explicitly. */

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

export type HypothesisStatus = "candidate" | "supported" | "rejected" | "unknown" | "confirmed";
export type VerificationStatus = "success" | "partial" | "failed" | "regressed" | "unknown";

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

export interface ResourceRef {
  tenant_id: UUID;
  cluster_id: UUID;
  resource_type: string;
  namespace: string | null;
  name: string;
  resource_id: string;
}

export interface StructuredError {
  error_code: string;
  message: string;
  retryable: boolean;
  fields: Record<string, string>;
}

export interface ToolResult {
  tool_name: string;
  success: boolean;
  status: ToolStatus;
  summary: string;
  data: unknown;
  error: StructuredError | null;
  evidence_ids: UUID[];
  started_at: string;
  finished_at: string;
}

export interface Evidence {
  id: UUID;
  run_id: UUID;
  source: string;
  evidence_type: EvidenceType;
  resource_id: string | null;
  cluster_id: UUID;
  namespace: string | null;
  timestamp: string | null;
  start_time: string | null;
  end_time: string | null;
  fact: string;
  confidence: number;
  severity: string | null;
  trace_id: string | null;
  raw_data: unknown;
  metadata: Record<string, unknown>;
}

export interface Hypothesis {
  id: UUID;
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
  action_type: string;
  target: ResourceRef;
  parameters: Record<string, unknown>;
  risk_level: 0 | 1 | 2 | 3 | 4;
  expected_effect: string;
  rollback: Record<string, unknown> | null;
}

export interface VerificationResult {
  status: VerificationStatus;
  before_snapshot: Record<string, unknown>;
  after_snapshot: Record<string, unknown>;
  observation_window_seconds: number;
  checks: Array<Record<string, unknown>>;
  summary: string;
}

export interface SSEEvent {
  event: string;
  run_id: UUID;
  sequence: number;
  timestamp: string;
  cluster_id: UUID;
  payload: Record<string, unknown>;
}
