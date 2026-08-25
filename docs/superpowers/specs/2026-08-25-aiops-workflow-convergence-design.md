# AIOps Workflow Convergence Design

**Date:** 2026-08-25  
**Status:** APPROVED by user on 2026-08-25  
**Supersedes as an implementation baseline:** completion claims in `2026-08-24-a1-d-runtime-convergence.md`, `2026-08-25-c2-controlled-ai-investigation.md`, and `2026-08-25-v2-p0-runtime-correctness.md` where those claims conflict with the current working tree.

## Goal

Converge the repository on one persistent, recoverable, evidence-backed AIOps investigation workflow in which Query API/MySQL owns control-plane truth, Orchestrator is a lease-fenced worker, and Action Executor is the only mutation boundary.

## Current Code Facts

1. Query API correctly creates `ai_runs` and `ai_run_outbox` in one transaction, but the dispatcher signs a new random `request_id` while the persisted `run_id` exists only in the request body.
2. Orchestrator `/internal/v1/run-invocations` ignores `body.run_id`, treats signed `request_id` as the Run ID, runs `mode="chat"`, and attempts `created -> success`.
3. Both Go and Python state machines reject `created -> success`; the endpoint catches commit failure and still returns HTTP 200, so the outbox can be marked delivered while the Run remains `created`.
4. The production investigation path calls legacy collection functions rather than `AgentRuntimeFramework -> RealToolExecutor -> InternalQueryClient`.
5. Internal query ToolRun wrapping is optional and some handlers ignore `beginToolRun` errors, allowing data-source I/O without a valid persistent ToolRun.
6. LangGraph, persistent `ai_runs`, SQLite `flow_runs`, legacy task APIs, and alert investigator paths implement overlapping execution semantics.
7. The independent Action Executor has the right default-deny direction, but Orchestrator still contains direct Shell/Kubernetes execution paths.
8. Investigation UI reads the Run and ToolRun list, but Plan, Hypothesis, Action, Verification, and live SSE progression are not connected to persistent data.

## Architectural Decision

Adopt incremental convergence rather than a patch-only repair or a rewrite.

- A patch-only repair would restore Run progress but leave evidence, recovery, workflow, and mutation split-brain unresolved.
- A rewrite would discard already useful MySQL, lease, signing, ToolRun, action, and SSE infrastructure and would materially increase migration risk.
- Incremental convergence preserves the correct substrate and creates independently testable release gates.

## Authority Boundaries

### Query API / MySQL

Query API is the only owner of:

- Run creation, state, version, lease, retry metadata, and terminal commit.
- Plan steps, ToolRuns, Evidence, hypotheses/RCA records, Run events, actions, approvals, execution attempts, and verification results.
- Invocation and command idempotency decisions.
- Browser authorization and tenant/cluster ownership enforcement.

### Orchestrator

Orchestrator is a stateless or reconstructable worker. It may:

- Accept a signed invocation for an existing Run.
- Claim and renew a Run lease.
- Plan steps and execute read-only tools through Query API.
- Produce hypotheses, RCA proposals, action proposals, and reports referencing persistent IDs.
- Request state transitions and terminal commits through the control-plane API.

It must not own a second production Run/Evidence store or directly mutate Kubernetes, databases, OpenStack, network devices, or the local shell.

### Action Executor

Action Executor is the only component allowed to perform a target mutation. It consumes a signed, immutable, approved Action context, rereads the target, applies UID/resourceVersion preconditions, records a durable execution attempt through Query API, and exposes reconcile/rollback outcomes.

### Frontend

The browser only calls Query API. It renders persistent Run projections and consumes Query API SSE replay/live events. It never derives Tool activity, Evidence, approval, or success from graph-node names or transient Orchestrator output.

## Canonical Investigation Flow

```text
User explicit create
  -> Query API transaction: Run(created) + Outbox(pending)
  -> Dispatcher signs existing run_id + invocation_id
  -> Orchestrator validates signed/body identity
  -> Synchronous accept: idempotency + lease claim + created->planning
  -> HTTP 202 accepted
  -> Background InvestigationRuntime
  -> planning->investigating
  -> PlanStep -> fenced ToolRun -> ToolResultEnvelope
  -> atomic ToolRun->Evidence consume
  -> Hypothesis/RCA using Evidence IDs only
  -> optional awaiting_confirmation / awaiting_approval
  -> Action Executor only when approved and enabled
  -> independent verification
  -> atomic terminal commit + persistent events
  -> Query API SSE replay/live delivery
```

## Invocation Contract

`RunInvocationContext` remains a short-lived signed context and adds two required UUIDs for `ai.investigate`:

```json
{
  "context_type": "run_invocation",
  "request_id": "correlation UUID",
  "run_id": "existing ai_runs.run_id",
  "invocation_id": "existing ai_run_outbox.invocation_id",
  "capability": "ai.investigate",
  "tenant_id": "tenant UUID",
  "cluster_scope": ["one canonical cluster UUID"]
}
```

For `ai.chat`, `run_id` and `invocation_id` are absent. The two modes use separate issuer methods so Chat cannot accidentally satisfy an Investigation ingress contract.

The request body carries the business input:

```json
{
  "run_id": "UUID",
  "invocation_id": "UUID",
  "request_id": "original idempotency/correlation UUID",
  "tenant_id": "UUID",
  "cluster_id": "UUID",
  "intent": "diagnosis",
  "resource_id": "svc/checkout",
  "service": "checkout",
  "message": "checkout error rate increased",
  "action_mode": "read_only"
}
```

Orchestrator rejects any signed/body mismatch before claiming a lease.

## Accept and Execution Semantics

The ingress request is an acceptance handshake, not the investigation execution lifetime.

1. Validate service credential, signature, capability, tenant, cluster, `run_id`, and `invocation_id`.
2. Use `invocation_id` as the stable lease claim/idempotency identity.
3. Claim the lease and transition `created -> planning` with CAS, or return the previously accepted response for the same invocation.
4. Place the Run in a bounded in-process queue and return HTTP 202.
5. A worker executes the Run. If the process dies after acceptance, lease expiry plus Recovery Scanner makes it eligible again.

Outbox becomes `delivered` only for HTTP 202 or an explicit idempotent-already-accepted response. Validation, lease, queue saturation, and control-plane errors remain retryable/non-delivered according to their HTTP class.

## State Machine

The only legal progression is the shared Go/Python state machine:

```text
created -> planning -> investigating
planning -> awaiting_confirmation | failed | cancelled
investigating -> awaiting_confirmation | awaiting_approval | verifying | failed | cancelled
awaiting_confirmation -> investigating | awaiting_approval | cancelled
awaiting_approval -> executing | failed | cancelled
executing -> verifying | success | partial | failed | regressed | cancelled
verifying -> success | partial | failed | regressed | cancelled
```

The approved read-only completion path is:

```text
investigating -> verifying -> success | partial | failed | regressed
```

Both state-machine tables and tests must be updated atomically to add `verifying` as an allowed target from `investigating`.

## ToolRun and Evidence Contract

Every Investigation data-source request requires a `ToolExecutionContext`:

```text
run_id, step_id, tool_run_id, idempotency_key,
executor_id, lease_epoch, lease_token,
query_window_start, query_window_end
```

The signed `TrustedRequestContext` also carries `workload_kind`, with exactly one of `investigation`, `chat`, or `platform`. This lets Query API enforce ToolRun requirements without treating platform maintenance calls as Investigation work. Live Chat data access remains disabled until a persistent ChatTool audit model exists.

Rules:

- Missing or invalid context for an `ai.investigate` request returns 422/409 before data-source I/O.
- ToolRun insert, idempotency lookup, and lease fencing errors are propagated; handlers never ignore them.
- Idempotency lookup is scoped by `(run_id, idempotency_key)`.
- `args_hash` is SHA-256 over canonical JSON containing operation and every semantic query parameter.
- Evidence is created only through Query API's atomic ToolRun consume endpoint.
- Production RCA and reports reference persistent Evidence IDs. In-memory `EvidenceHub` and `EvidenceRegistry` remain test utilities only and are not public production data sources.

## Chat Boundary

- Chat remains a separate `ai.chat` read-only interaction path.
- Pure conversation performs no live collection.
- Ordinary Chat diagnostics may perform narrowly scoped read-only queries only if they produce a separate ChatTool audit record; they do not masquerade as an Investigation Run.
- Structured RCA, cross-source evidence correlation, action proposals, and execution require an explicit Investigation Run.
- Chat never returns an executable script or calls a mutation endpoint.

## Workflow Convergence

- `ai_runs` is the sole runtime state store.
- The custom Flow engine may remain as a definition/editor/compiler, but its production execution and `flow_runs` state are disabled.
- Approval resume restores the persisted frontier; it never reruns the graph from entry.
- Alert webhooks register alert events. Automatic investigation is default-off and, when explicitly enabled by policy, creates a normal audited system Run through Query API rather than calling personas or Flow execution directly.
- Legacy task, suggestion execution, direct K8s action, and direct shell routes return 410 and are removed after one release of compatibility telemetry.

## Recovery and Events

- Recovery Scanner starts with Orchestrator and periodically queries Query API for non-terminal Runs without an active lease and outside retry backoff.
- A recovered Run reconstructs its next step from persistent PlanStep, ToolRun, Evidence, Action, and runtime metadata.
- Query API owns event sequence allocation and SSE replay. Orchestrator only appends events through signed control-plane calls.
- Recovery never repeats an eligible completed ToolRun or an Action with a known durable result.

## Action and Verification

- Production remains `EXECUTION_MODE=disabled` until all action gates pass.
- Action Executor is the only mutation owner; Orchestrator has no write RBAC and no mutation credentials.
- Execution attempts and reconcile outcomes are persisted in Query API/MySQL before UI success is shown.
- Verification is performed by an independent read-only observer using frozen pre-action and post-action windows.
- A constant `pass=true` result is forbidden. Failed verification produces `regressed` or `rollback_required` according to action policy.

## Rollout Gates

1. **G0 Run correctness:** one `run_id` from create through terminal commit; no stuck-`created` false delivery.
2. **G1 Evidence correctness:** every Investigation data-source I/O has a fenced ToolRun; every RCA reference resolves to persistent Evidence.
3. **G2 Recovery:** crashes at acceptance, collection, approval wait, execution, and verification recover without duplicate side effects.
4. **G3 Boundary convergence:** no production mutation path exists outside Action Executor; no second Run/Evidence authority is reachable.
5. **G4 UI truth:** browser state is reconstructable from Query API persistence and SSE replay alone.
6. **G5 Controlled action:** only after G0-G4, dry-run and then single-target approved mutation may be considered.

## Non-Goals

- No new message broker is introduced in the first convergence release; the existing transactional outbox plus lease/recovery model is sufficient.
- No general-purpose arbitrary shell execution is retained.
- No multi-cluster Run is added; one Run remains bound to one canonical cluster.
- No production mutation is enabled by this design or its implementation plan.

## Verification Strategy

- Test-first changes with a failing cross-service contract test before each protocol modification.
- Go package tests for Query API state, outbox, ToolRun, Evidence, and action persistence.
- Python tests for ingress, worker runtime, lease loss, recovery, and graph boundaries.
- Frontend type checks and component tests for SSE-derived state.
- A local end-to-end test must execute create -> dispatch -> accept -> ToolRun -> Evidence -> terminal commit -> SSE replay using the real HTTP handlers and a disposable database.
- Fault tests cover duplicate dispatch, response loss, process death, lease expiry, DB outage, queue saturation, stale action version, and verification regression.
