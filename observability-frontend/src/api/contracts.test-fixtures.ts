import type {
  Evidence,
  Hypothesis,
  OpsAction,
  ResourceRef,
  RunControlContext,
  RunInvocationContext,
  StructuredError,
  ToolResult,
  TrustedRequestContext,
  VerificationResult,
} from "./contracts";

const clusterA = "66666666-6666-4666-8666-666666666666";
const clusterB = "88888888-8888-4888-8888-888888888888";
const tenantId = "55555555-5555-4555-8555-555555555555";

export const runInvocationContextFixture = {
  version: 1,
  context_type: "run_invocation",
  issuer: "query-api",
  audience: "ai-orchestrator",
  request_id: "11111111-1111-4111-8111-111111111111",
  principal_type: "user",
  principal_id: "33333333-3333-4333-8333-333333333333",
  session_id: "44444444-4444-4444-8444-444444444444",
  tenant_id: tenantId,
  source: "frontend",
  cluster_scope: [clusterA],
  issued_at: "2026-08-19T10:00:00Z",
  expires_at: "2026-08-19T10:00:30Z",
  nonce: "77777777-7777-4777-8777-777777777777",
} satisfies RunInvocationContext;

export const runControlContextFixture = {
  version: 1,
  context_type: "run_control",
  issuer: "query-api",
  audience: "ai-orchestrator",
  request_id: "11111111-1111-4111-8111-111111111111",
  run_id: "22222222-2222-4222-8222-222222222222",
  operation: "cancel",
  principal_type: "user",
  principal_id: "33333333-3333-4333-8333-333333333333",
  session_id: "44444444-4444-4444-8444-444444444444",
  tenant_id: tenantId,
  action_id: null,
  decision_id: null,
  issued_at: "2026-08-19T10:00:00Z",
  expires_at: "2026-08-19T10:00:30Z",
  nonce: "77777777-7777-4777-8777-777777777777",
} satisfies RunControlContext;

export const trustedRequestContextFixture = {
  version: 1,
  context_type: "trusted_request",
  issuer: "ai-orchestrator",
  audience: "ai-apm-query-go",
  request_id: "11111111-1111-4111-8111-111111111111",
  run_id: "22222222-2222-4222-8222-222222222222",
  principal_type: "user",
  principal_id: "33333333-3333-4333-8333-333333333333",
  session_id: "44444444-4444-4444-8444-444444444444",
  tenant_id: tenantId,
  scope_kind: "cluster",
  cluster_id: clusterA,
  capability: "observability.logs.read",
  source: "log_agent",
  issued_at: "2026-08-19T10:00:00Z",
  expires_at: "2026-08-19T10:00:30Z",
  nonce: "77777777-7777-4777-8777-777777777777",
} satisfies TrustedRequestContext;

export const resourcesFixture = [
  {
    tenant_id: tenantId,
    cluster_id: clusterA,
    resource_type: "service",
    namespace: "production",
    name: "orders",
    resource_id: `service:${clusterA}:production:orders`,
  },
  {
    tenant_id: tenantId,
    cluster_id: clusterB,
    resource_type: "service",
    namespace: "production",
    name: "orders",
    resource_id: `service:${clusterB}:production:orders`,
  },
] satisfies ResourceRef[];

export const toolResultSuccessFixture = {
  tool_name: "query_k8s",
  cluster_id: clusterA,
  success: true,
  status: "success",
  summary: "deployment is healthy",
  data: { ready_replicas: 3, desired_replicas: 3 },
  error_code: null,
  error_message: null,
  retryable: false,
  evidence_ids: ["99999999-9999-4999-8999-999999999999"],
  source_system: "kubernetes",
  query_id: "qry-1",
  time_range: { start: "2026-08-19T09:00:00Z", end: "2026-08-19T10:00:00Z" },
  started_at: "2026-08-19T10:00:01Z",
  finished_at: "2026-08-19T10:00:02Z",
} satisfies ToolResult;

export const toolResultEmptyNoDataFixture = {
  tool_name: "k8sgpt_diagnose",
  cluster_id: clusterA,
  success: true,
  status: "no_data",
  summary: "k8sgpt executed successfully but produced no diagnostics",
  data: [],
  error_code: null,
  error_message: null,
  retryable: false,
  evidence_ids: [],
  source_system: "k8sgpt",
  query_id: null,
  time_range: null,
  started_at: "2026-08-19T10:00:01Z",
  finished_at: "2026-08-19T10:00:02Z",
} satisfies ToolResult;

export const evidenceFixture = {
  evidence_id: "99999999-9999-4999-8999-999999999999",
  run_id: "22222222-2222-4222-8222-222222222222",
  tenant_id: tenantId,
  cluster_id: clusterA,
  evidence_type: "k8s_state",
  claim_type: "fact",
  source: "query_k8s",
  source_reliability: 0.95,
  resource_id: `service:${clusterA}:production:orders`,
  namespace: "production",
  service: "orders",
  pod: null,
  node: null,
  trace_id: null,
  observed_at: "2026-08-19T10:00:02Z",
  time_range_start: "2026-08-19T09:00:00Z",
  time_range_end: "2026-08-19T10:00:00Z",
  fact: "orders has all desired replicas",
  raw_ref: null,
  raw_digest_sha256: "abc123",
  metadata: { ready_replicas: 3 },
  provenance_fingerprint: "fp-1",
  created_at: "2026-08-19T10:00:02Z",
} satisfies Evidence;

export const hypothesisFixture = {
  hypothesis_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  run_id: "22222222-2222-4222-8222-222222222222",
  title: "no active deployment failure",
  description: "The target workload is currently healthy.",
  supporting_evidence: [evidenceFixture.evidence_id],
  contradicting_evidence: [],
  missing_evidence: [],
  confidence: 0.82,
  status: "confirmed",
} satisfies Hypothesis;

export const opsActionFixture = {
  action_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  run_id: "22222222-2222-4222-8222-222222222222",
  tenant_id: tenantId,
  cluster_id: clusterA,
  target_resource_id: `deployment:${clusterA}:production:orders`,
  resource_version: "123456",
  action_type: "restart_workload",
  parameters: {},
  proposed_risk: "R2",
  authoritative_risk: "R2",
  expected_effect: "recreate workload pods",
  verification_policy_id: "vp-restart",
  rollback_strategy: null,
  action_hash: "hash-1",
  idempotency_key: "idem-1",
  created_by: "33333333-3333-4333-8333-333333333333",
  created_at: "2026-08-19T10:00:03Z",
} satisfies OpsAction;

export const verificationFixture = {
  verification_id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
  run_id: "22222222-2222-4222-8222-222222222222",
  action_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  status: "success",
  before_snapshot: { ready_replicas: 3 },
  after_snapshot: { ready_replicas: 3 },
  observation_window_seconds: 120,
  checks: [{ name: "ready_replicas", passed: true }],
  summary: "workload remained healthy",
} satisfies VerificationResult;

export const clusterIdentityMismatchErrorFixture = {
  error_code: "CLUSTER_IDENTITY_MISMATCH",
  message:
    "credential resolved to a Kubernetes cluster whose identity differs from the registration binding",
  retryable: false,
  fields: {
    cluster_id: clusterA,
    expected_identity: "uid-kube-system-cluster-a",
    observed_identity: "uid-kube-system-cluster-b",
  },
} satisfies StructuredError;

export const sameNameDifferentClusters =
  resourcesFixture[0].name === resourcesFixture[1].name &&
  resourcesFixture[0].cluster_id !== resourcesFixture[1].cluster_id;
