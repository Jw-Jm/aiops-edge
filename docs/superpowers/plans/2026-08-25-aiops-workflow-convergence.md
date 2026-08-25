# AIOps Workflow Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the split and partially disconnected AIOps paths with one persistent, lease-fenced, evidence-backed Investigation workflow while keeping production mutation disabled.

**Architecture:** Query API/MySQL is the sole control-plane authority. Orchestrator accepts signed invocations for existing Runs and executes them as a reconstructable worker through Query API ToolRun/Evidence APIs. Action Executor is the only mutation boundary and remains disabled until the read-only investigation gates pass.

**Tech Stack:** Go, Python 3.14/FastAPI/LangGraph, React/TypeScript, MySQL migrations, Helm, Go `testing`/`httptest`/`sqlmock`, and `pytest`.

**Spec:** `docs/superpowers/specs/2026-08-25-aiops-workflow-convergence-design.md`

## Global Constraints

- Query API/MySQL is the only production owner of Run, PlanStep, ToolRun, Evidence, Event, Action, Approval, execution result, and verification state.
- One Investigation Run is bound to one canonical tenant and one canonical cluster.
- Every Investigation data-source I/O requires an active lease and a persistent ToolRun.
- Orchestrator must not receive MySQL/ClickHouse credentials or Kubernetes write RBAC.
- Action Executor remains `EXECUTION_MODE=disabled` throughout this plan.
- Browser traffic terminates at Query API.
- Existing completion notes are historical evidence, not acceptance results; only tests added here determine completion.
- Make each behavior change test-first. Preserve unrelated dirty-worktree changes.
- Do not edit applied migrations; resolve the current migration head before adding one.

## File Ownership

- `ai-apm-query-go/internal/contract/context.go`: signed context schema.
- `ai-apm-query-go/internal/api/run_dispatch.go`: outbox acceptance handshake.
- `ai-apm-query-go/internal/api/internal_query*.go`: pre-I/O ToolRun/lease enforcement.
- `ai-apm-query-go/internal/store/*`: persistent Run, ToolRun, Evidence, Action and Verification truth.
- `ai-orchestrator/investigation_dispatcher.py`: bounded queue and recovery scheduling.
- `ai-orchestrator/investigation_runtime.py`: legal Run lifecycle and agent coordination.
- `ai-orchestrator/tool_execution_context.py`: immutable tool/lease identity.
- `ai-action-executor/main.go`: sole target mutation adapter.
- `observability-frontend/src/pages/investigation/*`: persistent projection and SSE UI.

---

### Task 1: Bind the Signed Invocation to the Existing Run

**Files:**
- Modify: `ai-apm-query-go/internal/contract/context.go:112-155`
- Modify: `ai-apm-query-go/internal/auth/issuer.go:54-78`
- Modify: `ai-apm-query-go/internal/api/settings.go:724-752`
- Modify: `ai-apm-query-go/internal/api/run_dispatch.go:95-134`
- Modify: `ai-apm-query-go/internal/api/runs_public.go:46-141`
- Modify: `ai-orchestrator/contracts.py:235-245`
- Modify: `ai-orchestrator/main.py:583-679`
- Test: `ai-apm-query-go/internal/contract/context_test.go`
- Test: `ai-apm-query-go/internal/api/run_dispatch_test.go`
- Create: `ai-orchestrator/tests/test_run_invocation_identity.py`

**Interfaces:**
- `SignChatInvocation(principalType, principalID, sessionID, tenantID, source string, clusterScope []string, now time.Time)` signs `ai.chat` without Run identity.
- `SignExistingRunInvocation(runID, invocationID, requestID, tenantID, clusterScope, now)` signs `ai.investigate`.
- `request_id` remains correlation identity; it is never used as `run_id`.

- [ ] **Step 1: Write failing Go validation tests**

```go
func TestInvestigationInvocationRequiresExistingRunIdentity(t *testing.T) {
    ctx := validInvocationContext(t)
    ctx.Capability = "ai.investigate"
    if err := ctx.Validate(); err == nil { t.Fatal("missing run identity accepted") }
    ctx.RunID, ctx.InvocationID = runID, invocationID
    if err := ctx.Validate(); err != nil { t.Fatal(err) }
}

func TestChatInvocationRejectsRunIdentity(t *testing.T) {
    ctx := validInvocationContext(t)
    ctx.Capability, ctx.RunID, ctx.InvocationID = "ai.chat", runID, invocationID
    if err := ctx.Validate(); err == nil { t.Fatal("chat run identity accepted") }
}
```

- [ ] **Step 2: Run the tests and confirm they fail because the fields do not exist**

```bash
cd ai-apm-query-go
go test ./internal/contract ./internal/auth -run 'Invocation' -count=1
```

- [ ] **Step 3: Add matching Go and Python fields and validators**

```go
type RunInvocationContext struct {
    commonContext
    Source string `json:"source"`
    ClusterScope []string `json:"cluster_scope"`
    Capability string `json:"capability,omitempty"`
    RunID string `json:"run_id,omitempty"`
    InvocationID string `json:"invocation_id,omitempty"`
}
```

For `ai.investigate`, validate both IDs as UUIDs. For `ai.chat`, require both to be empty. Mirror those rules in `contracts.RunInvocationContext` with `Optional[UUID]` fields and a Pydantic model validator.

- [ ] **Step 4: Split the issuer and migrate both call sites**

```go
signed, err := issuer.SignExistingRunInvocation(
    run.RunID, o.InvocationID, run.RequestID, run.TenantID,
    []string{run.PrimaryClusterID}, time.Now(),
)
```

Use `SignChatInvocation` only in `ProxyChat`. Remove the generic exported issuer after all callers compile.

- [ ] **Step 5: Preserve resource and symptom through persistence and dispatch**

Add `ResourceID string \`json:"resource_id"\`` to the public request and persist:

```go
TargetType: "service",
TargetResourceID: firstNonEmpty(req.ResourceID, req.Service),
```

Dispatch `resource_id`, `service=TargetResourceID`, and `message=Intent` in addition to the existing fields.

- [ ] **Step 6: Write failing Python mismatch tests, then fix ingress**

```python
def test_signed_and_body_run_id_must_match(client, signed_invocation):
    body = {**signed_invocation.body, "run_id": str(uuid.uuid4())}
    response = client.post("/internal/v1/run-invocations",
                           headers=signed_invocation.headers, json=body)
    assert response.status_code == 403
    assert response.json()["detail"] == "RUN_ID_MISMATCH"
```

Set `run_id = str(claims["run_id"])` and reject Run, invocation, tenant, and cluster mismatches before lease or data access.

- [ ] **Step 7: Run focused tests**

```bash
cd ai-apm-query-go
go test ./internal/contract ./internal/auth ./internal/api -run 'Invocation|RunDispatch' -count=1
cd ../ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_run_invocation_identity.py tests/test_p19_chat_ingress.py
```

- [ ] **Step 8: Commit Task 1**

```bash
git add ai-apm-query-go/internal/contract/context.go ai-apm-query-go/internal/auth/issuer.go \
  ai-apm-query-go/internal/api/settings.go ai-apm-query-go/internal/api/run_dispatch.go \
  ai-apm-query-go/internal/api/runs_public.go ai-orchestrator/contracts.py ai-orchestrator/main.py \
  ai-orchestrator/tests/test_run_invocation_identity.py
git commit -m "fix: bind investigation invocation to persisted run"
```

---

### Task 2: Replace Synchronous Investigation HTTP with Recoverable Acceptance

**Files:**
- Create: `ai-orchestrator/investigation_dispatcher.py`
- Create: `ai-orchestrator/investigation_runtime.py`
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-orchestrator/control_plane_client.py`
- Modify: `ai-orchestrator/lease_aware_execution.py`
- Modify: `ai-apm-query-go/internal/store/ai_runs.go:384-414`
- Modify: `ai-apm-query-go/internal/api/control_plane_runs.go:151-178`
- Modify: `ai-apm-query-go/internal/api/run_dispatch.go:137-170`
- Create: `ai-orchestrator/tests/test_investigation_dispatcher.py`
- Create: `ai-orchestrator/tests/test_investigation_runtime.py`
- Create: `ai-orchestrator/tests/test_investigation_recovery_startup.py`

**Interfaces:**
- `InvestigationDispatcher.accept(AcceptedInvocation) -> AcceptResult` returns before investigation completion.
- `InvestigationRuntime.accept(AcceptedInvocation) -> AcceptedWork` claims the lease and advances `created -> planning` before HTTP 202.
- `InvestigationRuntime.execute(AcceptedWork) -> None` owns the remaining legal state progression and reuses the accepted lease handle.
- HTTP success is `202 {run_id, invocation_id, accepted:true}`.
- Both state machines add the legal read-only transition `investigating -> verifying`.

- [ ] **Step 1: Write failing queue/idempotency tests**

```python
@pytest.mark.asyncio
async def test_accept_returns_before_worker_finishes():
    runtime = BlockingRuntime()
    dispatcher = InvestigationDispatcher(runtime, capacity=2)
    await dispatcher.start()
    result = await dispatcher.accept(invocation("run-a", "inv-a"))
    assert result.accepted is True
    assert runtime.finished is False
    assert runtime.control_plane.status == "planning"

@pytest.mark.asyncio
async def test_duplicate_invocation_is_queued_once():
    dispatcher = InvestigationDispatcher(RecordingRuntime(), capacity=2)
    await dispatcher.start()
    await dispatcher.accept(invocation("run-a", "inv-a"))
    await dispatcher.accept(invocation("run-a", "inv-a"))
    assert dispatcher.queued_count("inv-a") == 1
```

- [ ] **Step 2: Run tests and confirm the new modules are missing**

```bash
cd ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_investigation_dispatcher.py
```

- [ ] **Step 3: Implement a bounded `asyncio.Queue` dispatcher**

```python
@dataclass(frozen=True)
class AcceptedInvocation:
    run_id: str
    invocation_id: str
    request_id: str
    tenant_id: str
    cluster_id: str
    intent: str
    resource_id: str
    service: str
    message: str
    action_mode: str
```

Queue saturation raises `QueueSaturated` and maps to HTTP 503. An in-memory accepted map suppresses same-process duplicates; lease and persistent state provide cross-process fencing.

Reserve queue capacity first, then call `InvestigationRuntime.accept`. Queue `AcceptedWork`, not a bare invocation:

```python
@dataclass(frozen=True)
class AcceptedWork:
    invocation: AcceptedInvocation
    lease: LeaseHandle
    cursor: RunCursor

async def accept(self, item: AcceptedInvocation) -> AcceptResult:
    async with self._accept_lock:
        if item.invocation_id in self._accepted:
            return self._accepted[item.invocation_id]
        if self._queue.full():
            raise QueueSaturated("investigation queue is full")
        work = await asyncio.to_thread(self._runtime.accept, item)
        self._queue.put_nowait(work)
        result = AcceptResult(item.run_id, item.invocation_id, True)
        self._accepted[item.invocation_id] = result
        return result
```

Initialize `_accept_lock = asyncio.Lock()` in the dispatcher constructor so two concurrent deliveries cannot both pass the capacity/idempotency check.

- [ ] **Step 4: Write failing legal-progression and commit-error tests**

```python
@pytest.mark.asyncio
async def test_read_only_run_uses_legal_progression():
    cp = RecordingControlPlane(status="created", version=0)
    runtime = InvestigationRuntime(cp, SuccessfulRunner())
    work = runtime.accept(invocation("run-a", "inv-a"))
    await runtime.execute(work)
    assert cp.targets == ["planning", "investigating", "verifying", "success"]

@pytest.mark.asyncio
async def test_terminal_commit_error_is_not_swallowed():
    with pytest.raises(ControlPlaneError):
        await InvestigationRuntime(FailingCommitControlPlane(), SuccessfulRunner()).execute(
            invocation("run-a", "inv-a"))
```

- [ ] **Step 5: Implement `RunCursor` and the runtime**

```python
@dataclass
class RunCursor:
    status: str
    version: int

    def apply(self, response: dict) -> None:
        self.status = response["run"]["status"]
        self.version = int(response["run"]["state_version"])
```

Use response versions for every next CAS. Never call `brain.stream_sync` with `mode="chat"` from the runtime.

Add `LeaseAwareExecutor.acquire(run_id: str, tenant_id: str, owner_id: str, claim_id: str, lease_seconds: int = DEFAULT_LEASE_SECONDS) -> LeaseHandle`. `LeaseHandle` starts renew immediately, exposes `check_active`, `commit`, and `close`, and is always closed by the worker in `finally`. Recovery uses a fresh recovery claim after the prior lease expires.

- [ ] **Step 6: Update both state tables atomically**

```go
"investigating": {"awaiting_confirmation", "awaiting_approval", "verifying", "failed", "cancelled"},
```

```python
"investigating": frozenset({"awaiting_confirmation", "awaiting_approval", "verifying", "failed", "cancelled"}),
```

Add a parity test that fails when the Go/Python transition sets diverge.

- [ ] **Step 7: Wire startup, recovery and HTTP 202 semantics**

Start the dispatcher worker and Recovery Scanner on FastAPI startup and await shutdown. Mark outbox delivered only for 202 or explicit idempotent acceptance. Validation, lease and queue failures remain non-delivered.

- [ ] **Step 8: Run Task 2 tests**

```bash
cd ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_investigation_dispatcher.py \
  tests/test_investigation_runtime.py tests/test_investigation_recovery_startup.py \
  tests/test_lease_aware_execution.py tests/test_recovery_policy.py
cd ../ai-apm-query-go
go test ./internal/store ./internal/api -run 'Transition|RunDispatch|Recovery|Lease|Commit' -count=1
```

- [ ] **Step 9: Commit Task 2**

```bash
git add ai-orchestrator/investigation_dispatcher.py ai-orchestrator/investigation_runtime.py \
  ai-orchestrator/main.py ai-orchestrator/control_plane_client.py ai-orchestrator/lease_aware_execution.py \
  ai-orchestrator/tests/test_investigation_dispatcher.py ai-orchestrator/tests/test_investigation_runtime.py \
  ai-orchestrator/tests/test_investigation_recovery_startup.py ai-apm-query-go/internal/store/ai_runs.go \
  ai-apm-query-go/internal/api/control_plane_runs.go ai-apm-query-go/internal/api/run_dispatch.go
git commit -m "feat: add recoverable investigation acceptance runtime"
```

---

### Task 3: Require a Fenced ToolRun Before Investigation Data I/O

**Files:**
- Create: `ai-orchestrator/tool_execution_context.py`
- Modify: `ai-orchestrator/internal_query_client.py:118-190`
- Modify: `ai-orchestrator/agent_runtime_integration.py:29-64`
- Modify: `ai-orchestrator/agent_runtime.py:56-125`
- Modify: `ai-orchestrator/trusted_context_issuer.py:45-95`
- Modify: `ai-orchestrator/contracts.py:264-285`
- Modify: `ai-apm-query-go/internal/contract/context.go:201-254`
- Modify: `ai-apm-query-go/internal/api/internal_query_envelope.go:39-91`
- Modify: `ai-apm-query-go/internal/api/internal_query.go:29-395`
- Modify: `ai-apm-query-go/internal/api/toolrun_wrapper.go:69-160`
- Modify: `ai-apm-query-go/internal/store/ai_tool_runs.go:285-299`
- Create: `ai-orchestrator/tests/test_tool_execution_context.py`
- Create: `ai-apm-query-go/internal/api/internal_query_toolrun_test.go`

**Interfaces:**
- `ToolExecutionContext` contains Run, step, ToolRun, idempotency, executor, lease and query-window identity.
- Signed `TrustedRequestContext` adds `workload_kind=investigation|chat|platform`.
- Investigation requests without complete ToolRun context fail before repository/data-source calls.
- Store lookup becomes `GetByIdemKey(runID, idemKey string)`.

- [ ] **Step 1: Write a failing Python request-body test**

```python
def test_internal_query_sends_complete_fenced_context(client, context):
    client.query(tool_id="query_logs.v1", operation="logs", tenant_id=TENANT,
                 cluster_id=CLUSTER, params={"service": "checkout"}, context=context)
    body = client.http.calls[0].json
    assert body["run_id"] == context.run_id
    assert body["step_id"] == context.step_id
    assert body["tool_run_id"] == context.tool_run_id
    assert body["lease_epoch"] == context.lease_epoch
    assert body["lease_token"] == context.lease_token
```

- [ ] **Step 2: Add the immutable context and pass it Runtime -> Agent -> Executor -> Client**

```python
@dataclass(frozen=True)
class ToolExecutionContext:
    run_id: str
    step_id: str
    tool_run_id: str
    idempotency_key: str
    executor_id: str
    lease_epoch: int
    lease_token: str
    query_window_start: str
    query_window_end: str
```

Change `InternalQueryClient.query` to accept this object. Delete production use of `context_ref` to synthesize Run identity.

- [ ] **Step 3: Write failing Go pre-I/O tests**

```go
func TestMetricsRejectsMissingInvestigationToolContextBeforeIO(t *testing.T) {
    repo := &countingMetricsRepo{}
    h := handlerWithInvestigationContext(repo)
    rec := httptest.NewRecorder()
    h.InternalQueryMetrics(rec, signedMetricsRequestWithoutToolContext(t))
    if rec.Code != http.StatusUnprocessableEntity { t.Fatalf("status=%d", rec.Code) }
    if repo.calls != 0 { t.Fatalf("repository called %d times", repo.calls) }
}

func TestMetricsPropagatesBeginToolRunErrorBeforeIO(t *testing.T) {
    repo := &countingMetricsRepo{}
    h := handlerWithFailingToolDAO(repo)
    rec := httptest.NewRecorder()
    h.InternalQueryMetrics(rec, validFencedMetricsRequest(t))
    if rec.Code < 400 || repo.calls != 0 { t.Fatalf("status=%d calls=%d", rec.Code, repo.calls) }
}
```

- [ ] **Step 4: Add signed workload kind and expose signed Run ID to authorization**

```python
class TrustedRequestContext(_ContextBase):
    run_id: UUID
    workload_kind: Literal["investigation", "chat", "platform"]
```

Mirror `WorkloadKind string \`json:"workload_kind"\`` in Go `TrustedRequestContext`, validate the three exact values, and propagate it from `TrustedContextIssuer.build_claims`.

```go
type internalQueryCtx struct {
    TenantID string
    ClusterID string
    RunID string
    Capability string
    WorkloadKind string
}
```

For this release, `investigation` requires a ToolRun, `platform` uses its explicit platform path, and `chat` returns `CHAT_LIVE_QUERY_DISABLED` until a ChatTool audit model exists.

- [ ] **Step 5: Handle `beginToolRun` errors in all eight handlers**

Replace every ignored error with:

```go
trc, idempotent, err := h.beginToolRun(req, rctx.TenantID, rctx.ClusterID)
if err != nil { respondInternalQueryError(w, err); return }
if rctx.WorkloadKind == "investigation" && trc == nil {
    respondInternalQueryError(w, &internalQueryError{
        Code: contract.ErrorCodeValidationFailed,
        Message: "investigation requires fenced tool context",
    })
    return
}
```

ToolRun DAO/insert/fencing failures must return explicit errors, never `(nil, false, nil)`.

- [ ] **Step 6: Correct idempotency scope and hashing**

```go
row := conn.QueryRow(`SELECT tool_run_id, run_id, status, idempotency_key, args_hash
    FROM ai_tool_runs WHERE run_id = ? AND idempotency_key = ?`, runID, idemKey)
```

Hash canonical JSON containing operation, sorted services, query, namespace, since, minutes, hours, limit, offset, top_k and absolute windows. Do not encode integers as one byte.

- [ ] **Step 7: Run Task 3 tests**

```bash
cd ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_tool_execution_context.py \
  tests/test_p72_internal_query_client.py tests/test_p0_runtime_executor_integration.py \
  tests/test_ari12_tool_executor.py
cd ../ai-apm-query-go
go test ./internal/api ./internal/store -run 'InternalQuery|ToolRun|Idem|Fence' -count=1
```

- [ ] **Step 8: Commit Task 3**

```bash
git add ai-orchestrator/tool_execution_context.py ai-orchestrator/internal_query_client.py \
  ai-orchestrator/agent_runtime_integration.py ai-orchestrator/agent_runtime.py \
  ai-orchestrator/trusted_context_issuer.py ai-orchestrator/tests/test_tool_execution_context.py \
  ai-apm-query-go/internal/contract/context.go ai-apm-query-go/internal/api/internal_query_envelope.go \
  ai-apm-query-go/internal/api/internal_query.go ai-apm-query-go/internal/api/toolrun_wrapper.go \
  ai-apm-query-go/internal/store/ai_tool_runs.go ai-apm-query-go/internal/api/internal_query_toolrun_test.go
git commit -m "fix: require fenced tool runs before investigation queries"
```

---

### Task 4: Make Query API the Persistent Evidence Authority

**Files:**
- Modify: `ai-apm-query-go/internal/store/ai_evidence.go`
- Modify: `ai-apm-query-go/internal/api/runs_public.go`
- Modify: `ai-apm-query-go/cmd/api/main.go:322-355`
- Modify: `ai-apm-query-go/internal/api/settings.go:642-830`
- Modify: `ai-orchestrator/control_plane_client.py`
- Modify: `ai-orchestrator/agent_runtime.py`
- Modify: `ai-orchestrator/investigation_runtime.py`
- Modify: `ai-orchestrator/ai_runs_api.py:80-134`
- Create: `ai-apm-query-go/internal/api/runs_public_evidence_test.go`

**Interfaces:**
- `EvidenceDAO.ListByRun(runID)` and `EvidenceDAO.GetByID(runID, evidenceID)` read MySQL.
- Query API serves `GET /api/v1/ai/runs/{runID}/evidences[/evidenceID]` locally.
- Runtime calls `ControlPlaneClient.consume_tool_evidence(run_id, tool_run_id, tenant_id, cluster_id, evidence_id, evidence_type, source_ref, raw_digest_sha256, summary, metadata)` and stores returned Evidence IDs.
- Orchestrator in-memory Evidence endpoints are unavailable in production.

- [ ] **Step 1: Write failing DAO and public ownership tests**

```go
func TestGetRunEvidencesReadsPersistentRows(t *testing.T) {
    h, mock := evidenceHandler(t)
    expectOwnedRun(mock, runID, tenantID)
    expectEvidenceRows(mock, runID, evidenceID)
    rec := httptest.NewRecorder()
    h.GetRunEvidencesPublic(rec, authorizedRunRequest(t, runID, tenantID))
    if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), evidenceID) {
        t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
    }
}
```

- [ ] **Step 2: Implement persistent Evidence reads**

```go
func (d *EvidenceDAO) GetByID(runID, evidenceID string) (*Evidence, error) {
    row := storeDB().QueryRow(`SELECT evidence_id, run_id, tenant_id, cluster_id,
        evidence_type, source_ref, raw_ref, raw_digest_sha256, summary, metadata_json,
        provenance_fingerprint, collected_at FROM ai_evidence
        WHERE run_id = ? AND evidence_id = ?`, runID, evidenceID)
    // scan every selected column; translate sql.ErrNoRows to nil result
}
```

`ListByRun` uses the same columns ordered by `collected_at, evidence_id`.

- [ ] **Step 3: Register local public routes and remove Evidence proxying**

```go
mux.HandleFunc("GET /api/v1/ai/runs/{runID}/evidences", handler.GetRunEvidencesPublic)
mux.HandleFunc("GET /api/v1/ai/runs/{runID}/evidences/{evidenceID}", handler.GetRunEvidencePublic)
```

Each handler loads the Run and enforces JWT tenant ownership before reading Evidence.

- [ ] **Step 4: Consume eligible ToolRuns from the runtime**

```python
evidence = self._control_plane.consume_tool_evidence(
    run_id=ctx.run_id, tool_run_id=ctx.tool_run_id,
    tenant_id=invocation.tenant_id, cluster_id=invocation.cluster_id,
    evidence_id=str(uuid.uuid4()), evidence_type=evidence_type,
    source_ref=tool_id, raw_digest_sha256=result.digest,
    summary=result.summary, metadata=result.metadata,
)
```

Persist only returned `evidence_id` references in hypothesis/RCA inputs.

- [ ] **Step 5: Retire production in-memory Evidence reads**

Keep utility classes for isolated tests, but make Orchestrator public Evidence endpoints return:

```python
raise HTTPException(status_code=410, detail="EVIDENCE_MOVED_TO_QUERY_API")
```

- [ ] **Step 6: Run Evidence tests**

```bash
cd ai-apm-query-go
go test ./internal/store ./internal/api -run 'Evidence|RunEvidences' -count=1
cd ../ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_p0_runtime_executor_integration.py \
  tests/test_p0_evidence_isolation.py tests/test_evidence_registry_api.py
```

- [ ] **Step 7: Commit Task 4**

```bash
git add ai-apm-query-go/internal/store/ai_evidence.go ai-apm-query-go/internal/api/runs_public.go \
  ai-apm-query-go/internal/api/runs_public_evidence_test.go ai-apm-query-go/cmd/api/main.go \
  ai-apm-query-go/internal/api/settings.go ai-orchestrator/control_plane_client.py \
  ai-orchestrator/agent_runtime.py ai-orchestrator/investigation_runtime.py ai-orchestrator/ai_runs_api.py
git commit -m "feat: make query api the evidence authority"
```

---

### Task 5: Disable Parallel Workflow and Alert Execution Semantics

**Files:**
- Modify: `ai-orchestrator/flow_api.py`
- Modify: `ai-orchestrator/flow_engine/usecase.py:115-141`
- Modify: `ai-orchestrator/main.py:1725-1795`
- Modify: `ai-orchestrator/investigator.py:54-56`
- Modify: `ai-orchestrator/orchestrator.py:833-884`
- Modify: `ai-orchestrator/tests/test_flow_api.py`
- Modify: `ai-orchestrator/tests/test_flow_alert_dispatch.py`
- Modify: `ai-orchestrator/tests/test_chat_investigation_split.py`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`

**Interfaces:**
- Flow definition management remains available.
- Flow run/resume returns 410 when the legacy runtime is disabled.
- Alert ingestion registers events but does not invoke Flow or `maybe_investigate` directly.
- Operational diagnosis in Chat produces the explicit Investigation CTA.

- [ ] **Step 1: Write failing boundary tests**

```python
def test_flow_run_is_gone_when_legacy_runtime_disabled(client, monkeypatch):
    monkeypatch.setenv("LEGACY_FLOW_RUNTIME_ENABLED", "0")
    response = client.post("/api/v1/ai/workflows/flow-a/run", json={"type": "manual"})
    assert response.status_code == 410
    assert response.json()["detail"] == "FLOW_RUNTIME_MOVED_TO_AI_RUNS"

def test_alert_webhook_does_not_start_shadow_runtime(client, spies):
    response = client.post("/api/v1/ops/webhook",
                           json={"source": "HighErrorRate", "severity": "critical"})
    assert response.status_code == 200
    assert spies.run_flow.calls == []
    assert spies.maybe_investigate.calls == []
```

- [ ] **Step 2: Gate only Flow run/resume endpoints**

```python
def _require_legacy_runtime_enabled() -> None:
    if os.environ.get("LEGACY_FLOW_RUNTIME_ENABLED", "0") != "1":
        raise HTTPException(status_code=410, detail="FLOW_RUNTIME_MOVED_TO_AI_RUNS")
```

Keep definition create/read/update/delete behavior unchanged in this task.

- [ ] **Step 3: Remove webhook background execution mounts and default investigator off**

Delete the calls at `main.py:1749-1791`, keeping alert/task registration. Change `_enabled()` default to `0`; set both Helm values `LEGACY_FLOW_RUNTIME_ENABLED=0` and `INVESTIGATOR_ENABLED=0`.

- [ ] **Step 4: Route operational Chat diagnosis to an explicit Run**

```python
@pytest.mark.asyncio
async def test_operational_diagnosis_requires_explicit_run():
    result = await node_chat_classify({"user_message": "诊断 checkout 服务延迟",
                                       "intent": "diagnosis"})
    assert result["investigation_required"] is True
    assert result["chat_pure"] is False
```

Pure conversation remains direct Chat. No Chat path performs live data collection in this release.

- [ ] **Step 5: Run tests and render Helm**

```bash
cd ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_flow_api.py tests/test_flow_alert_dispatch.py \
  tests/test_chat_investigation_split.py tests/test_p19_chat_ingress.py
cd ../deploy/helm/aiops
helm lint .
helm template aiops . | rg 'LEGACY_FLOW_RUNTIME_ENABLED|INVESTIGATOR_ENABLED'
```

Expected rendered values: both `0`.

- [ ] **Step 6: Commit Task 5**

```bash
git add ai-orchestrator/flow_api.py ai-orchestrator/flow_engine/usecase.py ai-orchestrator/main.py \
  ai-orchestrator/investigator.py ai-orchestrator/orchestrator.py ai-orchestrator/tests/test_flow_api.py \
  ai-orchestrator/tests/test_flow_alert_dispatch.py ai-orchestrator/tests/test_chat_investigation_split.py \
  deploy/helm/aiops/values.yaml deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml
git commit -m "chore: disable parallel aiops runtimes"
```

---

### Task 6: Make Action Executor the Only Mutation Boundary

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0008_action_attempt_verification.sql`
- Create: `ai-apm-query-go/internal/store/ai_action_attempts.go`
- Create: `ai-apm-query-go/internal/store/ai_verifications.go`
- Modify: `ai-apm-query-go/internal/api/action_control.go`
- Modify: `ai-orchestrator/main.py:1191-1222` and direct K8s/task endpoints
- Modify: `ai-orchestrator/orchestrator.py:1321-1347,2162-2207`
- Modify: `ai-orchestrator/flow_engine/nodes_aiops.py:138-169`
- Modify: `ai-action-executor/main.go`
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/rbac.yaml`
- Create: `ai-orchestrator/tests/test_no_mutation_bypass.py`

**Interfaces:**
- Durable execution-attempt history is keyed by `(action_id, attempt_id)`.
- Verification records contain frozen before/after windows and `passed|failed|regressed|inconclusive`.
- Orchestrator mutation endpoints return 410 and never invoke shell/Kubernetes execution.

- [ ] **Step 1: Resolve migration head and add the next migration**

```bash
cd ai-apm-query-go
ls internal/store/migrations/versions | sort
```

Create the immediate next sequence with:

```sql
CREATE TABLE ai_action_attempts (
  attempt_id CHAR(36) PRIMARY KEY,
  action_id CHAR(36) NOT NULL,
  run_id CHAR(36) NOT NULL,
  status VARCHAR(32) NOT NULL,
  request_digest_sha256 CHAR(64) NOT NULL,
  result_json JSON NULL,
  error_code VARCHAR(64) NULL,
  started_at DATETIME(3) NOT NULL,
  completed_at DATETIME(3) NULL,
  UNIQUE KEY uq_action_attempt (action_id, attempt_id),
  KEY idx_action_attempt_run (run_id, started_at)
);

-- statement-breakpoint

CREATE TABLE ai_verifications (
  verification_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  action_id CHAR(36) NULL,
  before_window_start DATETIME(3) NOT NULL,
  before_window_end DATETIME(3) NOT NULL,
  after_window_start DATETIME(3) NOT NULL,
  after_window_end DATETIME(3) NOT NULL,
  outcome VARCHAR(32) NOT NULL,
  evidence_ids_json JSON NOT NULL,
  summary TEXT NULL,
  created_at DATETIME(3) NOT NULL,
  KEY idx_verification_run (run_id, created_at)
);
```

- [ ] **Step 2: Write failing mutation-bypass tests**

```python
def test_legacy_suggestion_execute_is_gone(client, monkeypatch):
    monkeypatch.setattr("subprocess.run", lambda *a, **k: (_ for _ in ()).throw(
        AssertionError("subprocess called")))
    response = client.post("/api/v1/ai/suggestion/execute",
                           json={"script": "kubectl get pods", "approved": True})
    assert response.status_code == 410

def test_flow_execute_requires_action_executor():
    result = nodes_aiops._execute(fake_context(), {"script": "kubectl get pods"})
    assert result["error_code"] == "ACTION_EXECUTOR_REQUIRED"
```

- [ ] **Step 3: Replace direct mutation routes/nodes**

```python
@app.post("/api/v1/ai/suggestion/execute")
def execute_suggestion_command(_: SuggestionRequest):
    raise HTTPException(status_code=410, detail="ACTION_EXECUTION_MOVED_TO_QUERY_API")
```

Apply the same boundary to direct K8s execute and legacy task approval execution. Agent/Flow nodes may produce Action proposals but cannot run commands.

- [ ] **Step 4: Persist every executor attempt**

Create a `running` attempt before calling the executor. Atomically update the attempt and `ai_actions.execution_status` from the response. Network timeout becomes `execution_unknown`. Reconcile creates a new attempt record linked to the same Action.

- [ ] **Step 5: Replace constant verification with an independent observer**

The observer uses fenced read-only ToolRuns and persists `ai_verifications`. Map outcomes:

```text
passed -> success
failed -> failed
regressed -> regressed or rollback_required
inconclusive -> partial
```

Delete the constant `{"pass": True}` implementation.

- [ ] **Step 6: Remove Orchestrator write RBAC**

Delete `grantK8sWrite` rules. After Helm rendering, Orchestrator must have no `patch`, `create`, `delete`, or `update` verbs.

- [ ] **Step 7: Run action boundary tests**

```bash
cd ai-apm-query-go
go test ./internal/store ./internal/api -run 'Action|Attempt|Verification' -count=1
cd ../ai-action-executor
go test ./... -count=1
cd ../ai-orchestrator
.venv314/bin/python -m pytest -q tests/test_no_mutation_bypass.py \
  tests/test_k8s_actions.py tests/test_ops_action_hub_execute.py
cd ../deploy/helm/aiops
helm lint .
helm template aiops . > /tmp/aiops-rendered.yaml
awk 'BEGIN { RS="---" } /kind: ClusterRole/ && /name: ai-orchestrator-ops/ { print }' \
  /tmp/aiops-rendered.yaml >/tmp/ai-orchestrator-role.yaml
! rg -n 'verbs:.*(patch|create|delete|update)' /tmp/ai-orchestrator-role.yaml
```

- [ ] **Step 8: Commit Task 6**

Stage the resolved migration filename and only the files listed in this task, then run:

```bash
git commit -m "security: make action executor the only mutation boundary"
```

---

### Task 7: Drive Investigation UI from Persistent Projection and SSE

**Files:**
- Modify: `ai-apm-query-go/internal/api/runs_public.go`
- Modify: `ai-apm-query-go/internal/api/sse_proxy.go`
- Modify: `observability-frontend/src/api/client.ts:95-146`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx:38-180`
- Modify: `observability-frontend/src/pages/investigation/NewInvestigation.tsx:16-34`
- Create: `observability-frontend/src/pages/investigation/runProjection.ts`
- Test: `ai-apm-query-go/internal/api/runs_public_test.go`
- Test: `ai-apm-query-go/internal/api/sse_proxy_test.go`

**Interfaces:**
- `POST /api/v1/ai/runs` returns top-level `CreateRunResponse`.
- Run detail returns persistent Plan, ToolRun, Evidence, Hypothesis/RCA, Action, Approval, Verification and event cursor data.
- Frontend reduces replay/live events into the same projection shape.

- [ ] **Step 1: Correct the frontend create type and navigation**

```ts
export interface CreateRunResponse {
  run_id: string
  request_id: string
  status: string
  created_at: string
}
export const createRun = (data: Record<string, unknown>) =>
  api.post<CreateRunResponse>('/ai/runs', data)
```

Navigate to `/investigation/${data.run_id}` after creation instead of returning to the list.

- [ ] **Step 2: Write failing Query API projection tests**

Insert one Run with PlanStep, ToolRun, Evidence, Action and Verification fixtures. Assert `GetRunPublic` returns each collection and `last_event_sequence`. The test must fail while the handler only returns `airunToMap(run)`.

- [ ] **Step 3: Build one persistent projection**

```json
{
  "run": {},
  "plan": [],
  "tools": [],
  "evidences": [],
  "hypotheses": [],
  "rca": null,
  "actions": [],
  "approvals": [],
  "verifications": [],
  "last_event_sequence": 0
}
```

Load all children after tenant ownership succeeds. Return missing singular data as `null`; never fabricate an Action or verification status.

- [ ] **Step 4: Implement the reducer and SSE reconnect cursor**

```ts
export function reduceRunEvent(state: InvestigationProjection, event: RunEvent) {
  switch (event.event_type) {
    case 'run.transitioned': return mergeRun(state, event.payload.run)
    case 'tool.completed': return upsert(state, 'tools', event.payload.tool, 'tool_run_id')
    case 'evidence.created': return upsert(state, 'evidences', event.payload.evidence, 'evidence_id')
    case 'verification.completed':
      return upsert(state, 'verifications', event.payload.verification, 'verification_id')
    default: return state
  }
}
```

Fetch the projection first, then connect SSE using `last_event_sequence`; reconnect from the latest observed sequence.

Use authenticated `fetch`, not native `EventSource`, because the endpoint requires the JWT header:

```ts
export async function streamRunEvents(runId: string, after: number, signal: AbortSignal,
                                      onEvent: (event: RunEvent) => void) {
  const token = localStorage.getItem('token') ?? ''
  const response = await fetch(`/api/v1/ai/runs/${encodeURIComponent(runId)}/events?after_sequence=${after}`, {
    headers: { Authorization: `Bearer ${token}`, 'X-Tenant-ID': TENANT_ID }, signal,
  })
  if (!response.ok || !response.body) throw new Error(`SSE_HTTP_${response.status}`)
  await parseSseStream(response.body, (frame) => {
    if (frame.event === 'run_event') onEvent(JSON.parse(frame.data) as RunEvent)
  })
}
```

Implement `parseSseStream` in `runProjection.ts` using `TextDecoder`, a line buffer, blank-line frame termination, and `event:`/`data:` field parsing. Abort the prior stream in the effect cleanup before reconnecting.

- [ ] **Step 5: Delete hardcoded frontend defaults**

Remove hardcoded `plan: []`, `hypothesis: []`, Action `created`, and sample execution-status rows. Render only server-provided state.

- [ ] **Step 6: Run tests and build**

```bash
cd ai-apm-query-go
go test ./internal/api -run 'RunPublic|SSE|Projection' -count=1
cd ../observability-frontend
npm run build
```

- [ ] **Step 7: Commit Task 7**

```bash
git add ai-apm-query-go/internal/api/runs_public.go ai-apm-query-go/internal/api/runs_public_test.go \
  ai-apm-query-go/internal/api/sse_proxy.go ai-apm-query-go/internal/api/sse_proxy_test.go \
  observability-frontend/src/api/client.ts observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx \
  observability-frontend/src/pages/investigation/NewInvestigation.tsx \
  observability-frontend/src/pages/investigation/runProjection.ts
git commit -m "feat: render investigation state from persistent events"
```

---

### Task 8: Add the Cross-Service Release Gate and Fault Tests

**Files:**
- Create: `ai-apm-query-go/internal/api/investigation_e2e_test.go`
- Create: `ai-orchestrator/tests/test_investigation_http_contract.py`
- Create: `ai-orchestrator/tests/test_investigation_failure_recovery.py`
- Create: `deploy/scripts/verify-aiops-workflow-gates.sh`
- Create: `.github/workflows/aiops-workflow-gates.yml`

**Interfaces:**
- One command verifies G0-G4 and exits non-zero on any violation.
- The gate never enables G5 production mutation.

- [ ] **Step 1: Write a real-handler happy-path test**

Use disposable persistence and real HTTP handlers to execute:

```text
Create Run -> Outbox -> signed invocation -> HTTP 202 -> Lease
-> planning/investigating/verifying -> ToolRun -> Evidence
-> terminal commit -> SSE replay
```

Assert the same `run_id` at every boundary and a terminal MySQL row.

- [ ] **Step 2: Add response-loss and duplicate-delivery tests**

Simulate acceptance followed by dropped HTTP response. Redelivery with the same `invocation_id` must not create duplicate ToolRun, Evidence or terminal event rows.

- [ ] **Step 3: Add parameterized crash/recovery tests**

```python
@pytest.mark.parametrize("crash_after", [
    "accepted", "planning", "tool_started", "tool_completed",
    "evidence_consumed", "awaiting_approval", "action_unknown", "verifying",
])
async def test_recovery_does_not_repeat_committed_effects(crash_after):
    harness = RecoveryHarness.build(crash_after=crash_after)
    await harness.run_until_injected_crash()
    committed_before = await harness.snapshot_durable_ids()
    await harness.expire_active_lease()
    assert await harness.dispatcher.recover_once() == 1
    await harness.wait_for_terminal_or_deliberate_wait()
    await harness.assert_no_duplicate_ids(committed_before)
```

Implement `RecoveryHarness` in the same test file with the six methods shown above. Its snapshot contains ToolRun, Evidence, Action attempt and terminal-event IDs so the final assertion checks each durable side effect.

- [ ] **Step 4: Add fail-closed dependency cases**

Cover MySQL unavailable, ToolRun insert failure, lease renewal loss, queue saturation, invalid signature, tenant mismatch, cluster mismatch, stale resource version, executor timeout and inconclusive verification. Each case asserts a specific non-success code and zero unauthorized data-source/mutation calls.

- [ ] **Step 5: Create the verification script**

```bash
#!/usr/bin/env bash
set -euo pipefail
(cd ai-apm-query-go && go test ./... -count=1)
(cd ai-orchestrator && .venv314/bin/python -m pytest -q)
(cd ai-action-executor && go test ./... -count=1)
(cd observability-frontend && npm run build)
(cd deploy/helm/aiops && helm lint . && helm template aiops . >/tmp/aiops-workflow-gate.yaml)
! rg -n 'subprocess\.run\(.*shell=True|execute_shell\(' ai-orchestrator/main.py \
  ai-orchestrator/orchestrator.py ai-orchestrator/flow_engine
awk 'BEGIN { RS="---" } /kind: ClusterRole/ && /name: ai-orchestrator-ops/ { print }' \
  /tmp/aiops-workflow-gate.yaml >/tmp/ai-orchestrator-role.yaml
! rg -n 'verbs:.*(patch|create|delete|update)' /tmp/ai-orchestrator-role.yaml
```

Create `.github/workflows/aiops-workflow-gates.yml`:

```yaml
name: aiops-workflow-gates
on:
  workflow_dispatch:
  pull_request:
    paths:
      - 'ai-apm-query-go/**'
      - 'ai-orchestrator/**'
      - 'ai-action-executor/**'
      - 'observability-frontend/**'
      - 'deploy/helm/aiops/**'
      - 'deploy/scripts/verify-aiops-workflow-gates.sh'
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.25.x', cache: true }
      - uses: actions/setup-python@v5
        with: { python-version: '3.14', cache: 'pip' }
      - uses: actions/setup-node@v4
        with: { node-version: '22', cache: 'npm', cache-dependency-path: 'observability-frontend/package-lock.json' }
      - uses: azure/setup-helm@v4
        with: { version: 'v3.18.0' }
      - name: Install Python and frontend dependencies
        run: |
          python -m venv ai-orchestrator/.venv314
          ai-orchestrator/.venv314/bin/pip install -r ai-orchestrator/requirements.txt -r ai-orchestrator/requirements-dev.txt
          npm ci --prefix observability-frontend
      - name: Verify workflow gates
        run: deploy/scripts/verify-aiops-workflow-gates.sh
```

- [ ] **Step 6: Run the complete gate**

```bash
deploy/scripts/verify-aiops-workflow-gates.sh
```

Expected: exit 0. Persist test counts and configuration digests as CI artifacts.

- [ ] **Step 7: Perform a read-only shadow rollout**

Use:

```text
EXECUTION_MODE=disabled
LEGACY_FLOW_RUNTIME_ENABLED=0
INVESTIGATOR_ENABLED=0
INVESTIGATION_RUNTIME_ENABLED=1
```

Run one explicit Investigation against a non-critical service. Verify the same `run_id` in Query API logs, Orchestrator logs, `ai_runs`, `ai_tool_runs`, `ai_evidence` and SSE. Confirm Orchestrator has no database credentials and no write verbs.

- [ ] **Step 8: Commit Task 8**

```bash
git add ai-apm-query-go/internal/api/investigation_e2e_test.go \
  ai-orchestrator/tests/test_investigation_http_contract.py \
  ai-orchestrator/tests/test_investigation_failure_recovery.py \
  deploy/scripts/verify-aiops-workflow-gates.sh .github/workflows/aiops-workflow-gates.yml
git commit -m "test: gate the persistent aiops investigation workflow"
```

---

## Execution Order

1. Tasks 1-2 establish **G0 Run correctness**.
2. Tasks 3-4 establish **G1 Evidence correctness**.
3. Task 5 establishes parallel-runtime convergence.
4. Task 6 establishes mutation isolation and durable verification scaffolding without enabling mutation.
5. Task 7 establishes **G4 UI truth**.
6. Task 8 is the read-only release gate.

## Estimated Delivery

- Tasks 1-2: 3-5 engineering days.
- Tasks 3-4: 5-8 engineering days.
- Tasks 5-6: 4-7 engineering days.
- Tasks 7-8: 4-6 engineering days.
- Total: approximately 16-26 engineering days for one experienced engineer, excluding environment provisioning and production change approval.

## Stop Conditions

Stop the rollout and keep the system read-only if:

- Any boundary observes a `run_id` different from the persisted Run.
- A Run remains `created` after its outbox is delivered.
- Data-source I/O occurs without a persistent, lease-fenced ToolRun.
- Evidence exists without an eligible consumed ToolRun.
- Recovery repeats a ToolRun, Evidence consume, Action or terminal commit.
- UI success cannot be reconstructed from Query API persistence and event replay.
- Orchestrator retains a mutation credential, write RBAC verb or direct execution path.
