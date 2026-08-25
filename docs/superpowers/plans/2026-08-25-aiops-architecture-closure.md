# AIOps Architecture Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不重写现有可靠诊断底座的前提下，补齐 canonical Action 审批、执行、reconcile、验证、恢复、前端真相投影和生产隔离，使 Investigation 从创建到终态只有一条可恢复、可审计、无重复副作用的工作流。

**Architecture:** Query API/MySQL 继续拥有 Run、Action、Approval、Attempt、Verification 和 Event 的控制面真相；Orchestrator 只负责可重建的只读调查与验证；Action Executor 是唯一 mutation 边界。采用 expand-and-contract：先扩展 schema 与新 API，再双读验证、切换前端和 dispatcher，最后才删除 legacy `/ops/tasks` 与同步 `/execute` 路径。

**Tech Stack:** Go 1.x `net/http`/MySQL/sqlmock、Python 3.12/FastAPI/pytest/LangGraph、React 18/TypeScript/Vite、Kubernetes/Helm/NetworkPolicy、Ed25519 signed contexts。

**Spec:** `docs/superpowers/specs/2026-08-25-aiops-workflow-convergence-design.md`

## Global Constraints

- 实施基线为 Git 提交 `f1d7259`；开始执行前必须确认工作区状态并保存用户已有改动。
- Query API/MySQL 是 Run、Action、Approval、Attempt、Reconcile、Verification 与 Event 的唯一生产权威。
- Browser 只能调用 Query API；Orchestrator 不能直接审批、执行或持有 Kubernetes 写凭据。
- Action Executor 是唯一 mutation 边界；`EXECUTION_MODE=disabled` 与 `realMutation=false` 保持不变，直到 Task 11 的 G0-G5 全部通过。
- Action approval 必须绑定 canonical payload hash 与 action version；不得信任客户端提供的 approver、tenant、cluster、run 或 action hash。
- `lease_token`、Provider API key、Kubernetes credential 不得写入 Run、LangGraph checkpoint、Event payload、前端响应或日志。
- `execution_unknown` 只能通过真实目标读取收敛；未知、非 JSON、超时和读取失败不得映射为 success 或 reconciled。
- 不修改已经发布的 `0001` 至 `0008` migration；schema 变更只新增 `0009` 及后续 migration。
- 每项行为修复先加入失败测试，再写最小实现；每个 Task 独立提交，提交前运行其列出的验证命令。
- 第一版真实处置只允许 Kubernetes Deployment 的 `patch` 与 `scale`；现有 `restart` 从 executable allowlist 移除，等其 before/after/reconcile 语义单独设计后再开放。
- 不在本计划引入消息中间件；沿用 MySQL transactional outbox、lease、fencing 和周期 reconciler。

---

## Delivery Map

### 新建文件

- `ai-apm-query-go/internal/store/migrations/versions/0009_action_workflow_closure.sql`：Action version、幂等 payload hash、Action outbox、reconcile attempt 与 recovery 索引。
- `ai-apm-query-go/internal/contract/canonical_action.go`：canonical JSON、Action payload V2 和哈希计算。
- `ai-apm-query-go/internal/api/action_preflight.go`：只读解析目标 UID/resourceVersion 并生成 immutable Action。
- `ai-apm-query-go/internal/api/action_decision.go`：公共批准/驳回命令。
- `ai-apm-query-go/internal/api/action_command_service.go`：审批事务、Action outbox 与 Run 状态推进。
- `ai-apm-query-go/internal/api/action_dispatch.go`：Action outbox dispatcher。
- `ai-apm-query-go/internal/api/action_reconciler.go`：execution_unknown 周期收敛。
- `ai-apm-query-go/internal/api/run_projection.go`：统一 Run aggregate/read-model。
- `ai-apm-query-go/internal/store/ai_action_outbox.go`：Action command outbox DAO。
- `ai-apm-query-go/internal/store/ai_action_reconciliations.go`：reconcile attempt DAO。
- `ai-orchestrator/investigation_app.py`：无本地 checkpoint/PVC 的 Investigation Worker 入口。
- `observability-frontend/src/pages/admin/Approvals.test.tsx`：canonical 审批 UI 回归测试。
- `observability-frontend/src/api/runEvents.test.ts`：SSE replay/reconnect 纯客户端测试。
- `tests/workflow-e2e/`：跨服务 HTTP 合约和故障注入测试夹具。

### 重点修改文件

- Query API：`cmd/api/main.go`、`internal/api/action_control.go`、`action_executor_client.go`、`control_plane_actions.go`、`control_plane_runs.go`、`handler.go`、`settings.go`。
- Store：`ai_actions.go`、`ai_approval_decisions.go`、`ai_plan_steps.go`、`ai_hypotheses.go`、`ai_verifications.go`、`ai_run_lease.go`。
- Executor：`ai-action-executor/main.go`、`main_test.go`。
- Orchestrator：`main.py`、`control_plane_client.py`、`investigation_runtime.py`、`requirements.txt`。
- Frontend：`src/api/client.ts`、`pages/admin/Approvals.tsx`、`pages/investigation/IntelligentInvestigation.tsx`。
- Helm：`deploy/helm/aiops/values.yaml`、`values-prod.yaml`、`templates/networkpolicy.yaml`、`templates/ai-orchestrator/deployment.yaml`，并新增 Investigation Worker manifests。

---

### Task 1: Expand the durable workflow schema

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0009_action_workflow_closure.sql`
- Modify: `ai-apm-query-go/internal/store/migrations/schema_manifest_test.go`
- Modify: `ai-apm-query-go/internal/store/migrations/coverage_test.go`
- Test: `ai-apm-query-go/internal/store/migrations/migrator_test.go`

**Interfaces:**
- Consumes: existing `ai_actions`, `ai_approval_decisions`, `ai_action_attempts`, `ai_verifications`, `ai_runs`.
- Produces: Action payload V2 metadata, decision idempotency, action command outbox, durable reconciliation attempts and indexes consumed by Tasks 2-7.

- [x] **Step 1: Add a failing manifest test for migration 0009**

```go
func TestSchemaManifestIncludesActionWorkflowClosure(t *testing.T) {
    sql := migrationSQL(t, "0009_action_workflow_closure.sql")
    for _, required := range []string{
        "hash_schema_version", "action_version", "proposed_by",
        "decision_idempotency_key", "ai_action_outbox",
        "ai_action_reconciliations", "payload_hash",
    } {
        if !strings.Contains(sql, required) { t.Fatalf("migration missing %s", required) }
    }
}
```

- [x] **Step 2: Run the migration tests and confirm the missing migration failure**

Run: `cd ai-apm-query-go && go test ./internal/store/migrations -run 'SchemaManifest|Coverage|Migrator' -count=1`

Expected: FAIL because `0009_action_workflow_closure.sql` is absent.

- [x] **Step 3: Add the expand-only migration**

```sql
ALTER TABLE ai_actions
  ADD COLUMN hash_schema_version SMALLINT NOT NULL DEFAULT 1 AFTER action_hash,
  ADD COLUMN action_version BIGINT NOT NULL DEFAULT 1 AFTER hash_schema_version,
  ADD COLUMN proposed_by CHAR(36) NULL AFTER action_version,
  ADD COLUMN policy_version VARCHAR(64) NOT NULL DEFAULT 'action-policy-v1' AFTER proposed_by,
  ADD COLUMN preflight_status VARCHAR(32) NOT NULL DEFAULT 'unresolved' AFTER policy_version,
  ADD COLUMN target_resource_type VARCHAR(32) NOT NULL DEFAULT 'deployment' AFTER preflight_status,
  ADD UNIQUE KEY uq_ai_actions_id_version (action_id, action_version);

ALTER TABLE ai_approval_decisions
  -- Legacy rows keep NULL so an existing action with multiple historical
  -- decisions does not violate the new uniqueness rule during expansion.
  ADD COLUMN action_version BIGINT NULL AFTER action_hash,
  ADD COLUMN decision_idempotency_key VARCHAR(255) NULL AFTER action_version,
  ADD UNIQUE KEY uq_ai_approval_action_version (action_id, action_version),
  ADD UNIQUE KEY uq_ai_approval_decision_key (action_id, decision_idempotency_key);

ALTER TABLE ai_plan_steps ADD COLUMN payload_hash CHAR(64) NOT NULL DEFAULT '' AFTER parameters;
ALTER TABLE ai_hypotheses ADD COLUMN payload_hash CHAR(64) NOT NULL DEFAULT '' AFTER content;
ALTER TABLE ai_verifications ADD COLUMN payload_hash CHAR(64) NOT NULL DEFAULT '' AFTER checks_json;

CREATE TABLE ai_action_outbox (
  command_id CHAR(36) PRIMARY KEY,
  action_id CHAR(36) NOT NULL,
  action_version BIGINT NOT NULL,
  action_hash CHAR(64) NOT NULL,
  run_id CHAR(36) NOT NULL,
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'pending',
  dispatch_owner_id VARCHAR(255) NULL,
  dispatch_epoch BIGINT NOT NULL DEFAULT 0,
  dispatch_token_hash CHAR(64) NULL,
  dispatch_expires_at DATETIME(3) NULL,
  dispatch_count INT NOT NULL DEFAULT 0,
  next_retry_at DATETIME(3) NULL,
  delivered_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_action_outbox_action_version (action_id, action_version),
  INDEX idx_ai_action_outbox_pending (status, next_retry_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ai_action_reconciliations (
  reconciliation_id CHAR(36) PRIMARY KEY,
  action_id CHAR(36) NOT NULL,
  attempt_id CHAR(36) NOT NULL,
  action_hash CHAR(64) NOT NULL,
  status VARCHAR(24) NOT NULL,
  observed_uid VARCHAR(128) NOT NULL DEFAULT '',
  observed_version VARCHAR(128) NOT NULL DEFAULT '',
  observed_json JSON NULL,
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_action_reconcile_attempt (attempt_id),
  INDEX idx_ai_action_reconcile_action (action_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_ai_runs_recovery
  ON ai_runs(status, lease_expires_at, retry_not_before, created_at, run_id);
```

- [x] **Step 4: Register migration 0009 without modifying earlier migration checksums**

Update the ordered manifest so `0009_action_workflow_closure.sql` is applied after `0008_action_attempt_verification.sql`.

- [x] **Step 5: Run migration unit tests**

Run: `cd ai-apm-query-go && go test ./internal/store/migrations -count=1`

Expected: PASS.

- [x] **Step 6: Commit the schema expansion**

```bash
git add ai-apm-query-go/internal/store/migrations
git commit -m "feat: expand durable action workflow schema"
```

---

### Task 2: Define canonical Action V2 and perform trusted preflight

**Files:**
- Create: `ai-apm-query-go/internal/contract/canonical_action.go`
- Create: `ai-apm-query-go/internal/contract/canonical_action_test.go`
- Create: `ai-apm-query-go/internal/api/action_preflight.go`
- Create: `ai-apm-query-go/internal/api/action_preflight_test.go`
- Modify: `ai-apm-query-go/internal/query/kubernetes.go`
- Modify: `ai-apm-query-go/internal/api/kubernetes_boundary.go`
- Modify: `ai-apm-query-go/internal/api/control_plane_actions.go`
- Modify: `ai-apm-query-go/internal/store/ai_actions.go`
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-orchestrator/control_plane_client.py`

**Interfaces:**
- Consumes: proposal `{action_type, resource_type, namespace, target_name, operation, params}` from Orchestrator and the existing Kubernetes Access Boundary.
- Produces: `CanonicalActionPayloadV2`, `CanonicalActionHash`, `ActionPreflightService.Resolve`, and an immutable executable Action with `hash_schema_version=2`, `preflight_status=passed`, UID/resourceVersion and `dry_run=false`.

- [x] **Step 1: Write failing canonicalization tests**

```go
func TestCanonicalActionHashIncludesTargetAndParams(t *testing.T) {
    a := CanonicalActionPayloadV2{Version: 1, ActionType: "kubernetes",
        ResourceType: "deployment", Namespace: "prod", TargetName: "orders",
        TargetUID: "uid-1", ResourceVersion: "42", Operation: "scale",
        Params: json.RawMessage(`{"replicas":2}`), PolicyVersion: "action-policy-v1"}
    first, _ := CanonicalActionHash(a)
    a.ResourceVersion = "43"
    second, _ := CanonicalActionHash(a)
    if first == second { t.Fatal("resourceVersion must change the hash") }
}
```

- [x] **Step 2: Run the contract test and confirm the undefined type failure**

Run: `cd ai-apm-query-go && go test ./internal/contract -run CanonicalAction -count=1`

Expected: FAIL because `CanonicalActionPayloadV2` does not exist.

- [x] **Step 3: Implement canonical JSON and Action V2**

```go
type CanonicalActionPayloadV2 struct {
    Version         int64           `json:"version"`
    ActionType      string          `json:"action_type"`
    ResourceType    string          `json:"resource_type"`
    Namespace       string          `json:"namespace"`
    TargetName      string          `json:"target_name"`
    TargetUID       string          `json:"target_uid"`
    ResourceVersion string          `json:"resource_version"`
    Operation       string          `json:"operation"`
    Params          json.RawMessage `json:"params"`
    PolicyVersion   string          `json:"policy_version"`
}

func CanonicalActionHash(v CanonicalActionPayloadV2) (string, error) {
    normalized, err := NormalizeJSON(v)
    if err != nil { return "", err }
    sum := sha256.Sum256(normalized)
    return hex.EncodeToString(sum[:]), nil
}
```

`NormalizeJSON` must use `json.Decoder.UseNumber()`, decode into `any`, reject trailing data, then `json.Marshal`; this gives stable map-key ordering without adding a new dependency.

- [x] **Step 4: Add a failing preflight test using a fake boundary**

```go
func TestActionPreflightResolvesImmutableDeploymentIdentity(t *testing.T) {
    resolver := fakeActionTargetResolver{identity: KubeObjectIdentity{
        UID: "uid-1", ResourceVersion: "42", Namespace: "prod", Name: "orders"}}
    got, err := NewActionPreflightService(resolver).Resolve(context.Background(), PreflightInput{
        ClusterID: clusterID, ResourceType: "deployment", Namespace: "prod",
        TargetName: "orders", Operation: "scale", Params: json.RawMessage(`{"replicas":2}`)})
    if err != nil || got.HashSchemaVersion != 2 || got.ActionHash == "" { t.Fatalf("got %#v err=%v", got, err) }
}
```

- [x] **Step 5: Extend the existing Kubernetes boundary with one narrow read method**

```go
type KubeObjectIdentity struct {
    UID string; ResourceVersion string; Namespace string; Name string
    Observed json.RawMessage
}

type KubeClient interface {
    ClusterID() string
    ListNodeNames() ([]string, error)
    ListNodeDetails() ([]map[string]interface{}, error)
    ListPods(namespace string) ([]KubePod, error)
    GetDeploymentIdentity(namespace, name string) (KubeObjectIdentity, error)
}
```

Implement the adapter through the existing `k8sboundary.Client`; do not invoke a default kube-context or add a second credential path.

- [x] **Step 6: Make the internal Action append endpoint call preflight**

Reject unsupported resource types, missing namespace/name, `restart`, arbitrary scripts and unknown operations with HTTP 422. Persist the Action only after target resolution and hash calculation. Set `proposed_by` from the Run principal, not from the request body.

- [x] **Step 7: Change the Brain adapter to send structured candidates**

```python
candidate = {
    "action_type": "kubernetes",
    "resource_type": "deployment",
    "namespace": str(proposal.get("namespace") or ""),
    "target_name": str(proposal.get("target_name") or ""),
    "operation": str(proposal.get("operation") or ""),
    "params": proposal.get("params") or {},
}
```

If required target fields are absent, return `partial` with `ACTION_PREFLIGHT_REQUIRED`; do not transition the Run to `awaiting_approval`.

- [x] **Step 8: Run focused tests**

Run: `cd ai-apm-query-go && go test ./internal/contract ./internal/query ./internal/api -run 'CanonicalAction|ActionPreflight' -count=1`

Run: `cd ai-orchestrator && ./.venv312/bin/python -m pytest tests/test_investigation_runtime.py tests/test_p13_wiring.py -q`

Expected: PASS.

- [x] **Step 9: Commit canonical Action preflight**

```bash
git add ai-apm-query-go ai-orchestrator
git commit -m "feat: add canonical action preflight"
```

---

### Task 3: Add the canonical public approval decision command

**Files:**
- Create: `ai-apm-query-go/internal/api/action_decision.go`
- Create: `ai-apm-query-go/internal/api/action_decision_test.go`
- Create: `ai-apm-query-go/internal/api/action_command_service.go`
- Modify: `ai-apm-query-go/internal/api/action_control.go`
- Modify: `ai-apm-query-go/internal/api/auth.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`
- Modify: `ai-apm-query-go/internal/store/ai_approval_decisions.go`
- Modify: `ai-apm-query-go/internal/store/ai_actions.go`
- Create: `ai-apm-query-go/internal/store/ai_action_outbox.go`

**Interfaces:**
- Consumes: `POST /api/v1/ai/actions/{action_id}/decision` body `{decision, reason, idempotency_key, action_version}` and JWT-derived `AuthorizationContext`.
- Produces: one immutable approval decision and, when approved, one pending Action command plus atomic Run transition `awaiting_approval -> executing`.

- [x] **Step 1: Write handler tests for derived identity and stale hashes**

```go
func TestApproveActionDerivesApproverAndEnqueuesAtomically(t *testing.T) {
    req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/actions/a1/decision",
        strings.NewReader(`{"decision":"approved","reason":"reviewed","idempotency_key":"d1","action_version":1}`))
    req = withAuthorizationContext(req, AuthorizationContext{UserID: approverID, TenantID: tenantID})
    // SQL expectations: SELECT action+run FOR UPDATE, reject self approval,
    // INSERT decision with stored action_hash, UPDATE action, UPDATE run CAS,
    // INSERT ai_action_outbox, COMMIT.
    handler.ActionPublicHandler(rec, req)
    if rec.Code != http.StatusAccepted { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
}
```

Add separate tests for self-approval, cross-tenant access, stale `action_version`, duplicate idempotency key with same body, duplicate key with different body, and rejection.

- [x] **Step 2: Run tests and confirm the route is missing**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'ApproveAction|RejectAction|ActionDecision' -count=1`

Expected: FAIL with 404 or undefined service.

- [x] **Step 3: Define the public request without approver or action hash**

```go
type ActionDecisionRequest struct {
    Decision       string `json:"decision"`
    Reason         string `json:"reason"`
    IdempotencyKey string `json:"idempotency_key"`
    ActionVersion  int64  `json:"action_version"`
}
```

Only `approved` and `rejected` are accepted. `reason` is required for rejection. The handler obtains `Approver` from `AuthorizationContext.UserID` and tenant from `AuthorizationContext.TenantID`.

- [x] **Step 4: Implement one transaction in `ActionCommandService.Decide`**

```go
type ActionDecisionResult struct {
    ApprovalID string `json:"approval_id"`
    ActionID string `json:"action_id"`
    ActionVersion int64 `json:"action_version"`
    Decision string `json:"decision"`
    RunStatus string `json:"run_status"`
    CommandID string `json:"command_id,omitempty"`
}

func (s *ActionCommandService) Decide(ctx context.Context, actionID string,
    auth AuthorizationContext, req ActionDecisionRequest) (ActionDecisionResult, error)
```

Inside the transaction: lock Action and Run; verify tenant/run/cluster; require hash schema V2 and `preflight_status=passed`; reject `auth.UserID == action.ProposedBy`; compare action version; derive action hash from the row; insert decision; update Action status; CAS the Run. For approval, insert Action outbox in the same transaction. For rejection, transition Run to `cancelled` and append `action.rejected`.

- [x] **Step 5: Make duplicate decisions exact-replay only**

`AIApprovalDecisionDAO.CreateOrReplayTx` must return the stored row when `action_id + decision_idempotency_key` exists. If decision, action version, reason digest or approver differs, return `IDEMPOTENCY_KEY_REUSED` and map it to HTTP 409.

- [x] **Step 6: Add a MySQL-authoritative approval-role wrapper and register the route**

```go
func (h *Handler) RequireAnyRoleForWrite(roles []string, next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet && r.Method != http.MethodHead && !hasAnyRole(r, roles...) {
            respondJSON(w, http.StatusForbidden, map[string]any{"error": "permission_denied"})
            return
        }
        next(w, r)
    }
}
```

`hasAnyRole` must load the current user from MySQL exactly once and compare its stored role against `admin|approver`; JWT role claims and `X-Internal-Role` are ignored. `ActionPublicHandler` routes `POST /{id}/decision` before `/{id}/execute`. Protect Action list/detail/decision with the same `admin|approver` role policy because payloads contain sensitive remediation details.

- [x] **Step 7: Run API and store tests**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/store -run 'ActionDecision|Approval|ActionOutbox' -count=1`

Expected: PASS.

- [x] **Step 8: Commit the decision command**

```bash
git add ai-apm-query-go
git commit -m "feat: add canonical action approval command"
```

---

### Task 4: Dispatch approved Actions and drive Run state durably

**Files:**
- Create: `ai-apm-query-go/internal/api/action_dispatch.go`
- Create: `ai-apm-query-go/internal/api/action_dispatch_test.go`
- Modify: `ai-apm-query-go/internal/api/action_executor_client.go`
- Modify: `ai-apm-query-go/internal/api/action_control.go`
- Modify: `ai-apm-query-go/internal/store/ai_action_attempts.go`
- Modify: `ai-apm-query-go/internal/store/ai_action_outbox.go`
- Modify: `ai-apm-query-go/internal/store/ai_actions.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`

**Interfaces:**
- Consumes: pending `ai_action_outbox` rows and stored approved Action V2.
- Produces: one signed `ActionExecutionContext`, one durable Attempt, Action execution status and legal Run progression. The public `/execute` endpoint becomes enqueue/status semantics and never directly crosses the mutation boundary.

- [x] **Step 1: Write a lost-response dispatcher test**

```go
func TestActionDispatchLostResponseLeavesUnknownAndDoesNotRedeliverMutation(t *testing.T) {
    executor := newFakeExecutorThatAppliesThenDropsResponse()
    dispatchOneAction(command)
    assertActionStatus(t, "execution_unknown")
    assertAttemptCount(t, actionID, 1)
    dispatchOneAction(command)
    assertExecutorMutationCalls(t, executor, 1)
}
```

- [x] **Step 2: Run the test and confirm the action dispatcher is absent**

Run: `cd ai-apm-query-go && go test ./internal/api -run ActionDispatch -count=1`

Expected: FAIL.

- [x] **Step 3: Implement outbox claim fencing matching Run dispatch semantics**

`AIActionOutboxDAO` exposes `ScanPending(limit)`, `Claim(commandID, owner, lease)`, `Deliver(commandID, fence)` and `Retry(commandID, fence, nextRetry)`. `Deliver` and `Retry` must require owner, epoch and token hash from the current claim.

- [x] **Step 4: Persist Attempt before executor I/O**

Create the Attempt with `attempt_id=command_id`, `idempotency_key=action_id:action_version`, stored action hash and the exact signed request digest. A duplicate Attempt never calls Executor; it routes to Task 5 reconciliation.

Before signing, rebuild `CanonicalActionPayloadV2` from the locked Action row and require the recomputed hash to equal `action.ActionHash`. Pass parameters as `json.RawMessage(action.Params)`; do not `json.Marshal([]byte)` because that produces a base64 JSON string instead of the immutable object specification.

- [x] **Step 5: Map executor outcomes to Action and Run state**

```text
success           -> Action success           -> Run executing -> verifying
failed            -> Action failed            -> Run failed
rejected/drift     -> Action rejected          -> Run failed
rollback_required -> Action rollback_required -> Run regressed
timeout/no response/execution_unknown -> Action execution_unknown; Run stays executing
```

Each update appends a deterministic Run event in the same database transaction. Never transition Run to success directly from execution.

- [x] **Step 6: Change `/execute` to an idempotent compatibility endpoint**

If approved and no command exists, enqueue it. If a command exists, return current Action/Attempt status. Add `Deprecation: true` and `Sunset` response headers. Remove the synchronous `executeApprovedAction` call from the HTTP handler.

- [x] **Step 7: Start `RunActionDispatchLoop` only in the Query API dispatch role**

Use the same application context cancellation and shutdown behavior as `RunDispatchLoop`. Expose queue depth and oldest pending age metrics.

- [x] **Step 8: Run focused tests**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/store -run 'ActionDispatch|ActionAttempt|ActionPublic' -count=1`

Expected: PASS.

- [x] **Step 9: Commit durable Action dispatch**

```bash
git add ai-apm-query-go
git commit -m "feat: dispatch approved actions durably"
```

---

### Task 5: Replace synthetic reconcile with signed real-state reconciliation

**Files:**
- Modify: `ai-apm-query-go/internal/contract/context.go`
- Modify: `ai-apm-query-go/internal/api/action_executor_client.go`
- Create: `ai-apm-query-go/internal/api/action_reconciler.go`
- Create: `ai-apm-query-go/internal/api/action_reconciler_test.go`
- Create: `ai-apm-query-go/internal/store/ai_action_reconciliations.go`
- Modify: `ai-action-executor/main.go`
- Modify: `ai-action-executor/main_test.go`

**Interfaces:**
- Consumes: a signed immutable `ActionReconcileContext` and an `execution_unknown` Attempt.
- Produces: exactly one of `applied`, `not_applied`, `drift`, `unknown`; a durable reconciliation row; and a legal Action/Run transition.

- [x] **Step 1: Add executor tests for all four reconciliation outcomes**

```go
func TestReconcileScaleApplied(t *testing.T) {
    s := testServerWithDeployment(`{"metadata":{"uid":"u1","resourceVersion":"44"},"spec":{"replicas":2}}`)
    res := postSignedReconcile(t, s, reconcileContext("scale", `{"replicas":2}`))
    if res.Status != "applied" { t.Fatalf("status=%s", res.Status) }
}
```

Add cases for replicas unchanged (`not_applied`), UID changed (`drift`) and Kubernetes GET failure (`unknown`).

- [x] **Step 2: Run tests and confirm the current synthetic result**

Run: `cd ai-action-executor && go test ./... -run Reconcile -count=1`

Expected: FAIL because the handler returns `reconciled` without reading the target.

- [x] **Step 3: Define and validate the signed reconcile context**

```go
type ActionReconcileContext struct {
    ActionID string `json:"action_id"`
    AttemptID string `json:"attempt_id"`
    ActionHash string `json:"action_hash"`
    TargetUID string `json:"target_uid"`
    TargetName string `json:"target_name"`
    ResourceVersion string `json:"resource_version"`
    Namespace string `json:"namespace"`
    Operation string `json:"operation"`
    ExpectedSpec json.RawMessage `json:"expected_spec"`
}
```

Require all identity fields. Allow only `patch` and `scale`.

- [x] **Step 4: Implement real GET and operation-specific comparison**

For `scale`, compare `spec.replicas`. For the controlled `patch`, compare the exact executor-owned annotation. UID mismatch is `drift`; transport/decode/RBAC errors are `unknown`. Include observed UID/resourceVersion/object digest in the response, not the full Secret-bearing object.

- [x] **Step 5: Remove both false-reconciled fallbacks**

Delete the Executor's constant `reconciled` response and change Query API's non-JSON mapping from `reconciled` to `unknown` with `EXECUTOR_INVALID_RESPONSE`.

- [x] **Step 6: Persist reconciliation before changing Action state**

`ActionReconciler` inserts `ai_action_reconciliations`; exact duplicate attempt returns the stored result. Map outcomes:

```text
applied     -> Action success -> Run verifying
not_applied -> Action failed  -> Run failed
drift       -> Action rejected -> Run failed
unknown     -> keep Action execution_unknown and Run executing; retry with bounded backoff
```

- [x] **Step 7: Run Query API and Executor tests**

Run: `cd ai-action-executor && go test ./... -count=1`

Run: `cd ai-apm-query-go && go test ./internal/contract ./internal/api ./internal/store -run 'Reconcile|ExecutionUnknown' -count=1`

Expected: PASS.

- [x] **Step 8: Commit real reconciliation**

```bash
git add ai-action-executor ai-apm-query-go
git commit -m "fix: reconcile unknown actions from real target state"
```

---

### Task 6: Add independent verification and close the Run state machine

**Files:**
- Modify: `ai-apm-query-go/internal/api/control_plane_verifications.go`
- Modify: `ai-apm-query-go/internal/store/ai_verifications.go`
- Modify: `ai-apm-query-go/internal/api/control_plane_runs.go`
- Modify: `ai-orchestrator/investigation_runtime.py`
- Modify: `ai-orchestrator/control_plane_client.py`
- Create: `ai-orchestrator/verification_worker.py`
- Create: `ai-orchestrator/tests/test_verification_worker.py`
- Test: `ai-apm-query-go/internal/api/control_plane_verifications_test.go`

**Interfaces:**
- Consumes: a Run in `verifying`, a successful immutable Action and frozen before-window references.
- Produces: durable Verification with `passed|failed|regressed|inconclusive`; atomic Run terminal commit and events.

- [x] **Step 1: Add failing tests for truthful verification mapping**

```python
@pytest.mark.asyncio
async def test_regressed_verification_commits_regressed():
    outcome = await worker.verify(candidate_with(error_rate_before=.01, error_rate_after=.10))
    assert outcome.status == "regressed"
    assert cp.commits[-1]["terminal_status"] == "regressed"
```

Add `passed -> success`, failed observer -> `partial` or `failed` by policy, and missing post-window -> `inconclusive` without success.

- [x] **Step 2: Run focused tests and confirm no worker closes action verification**

Run: `cd ai-orchestrator && ./.venv312/bin/python -m pytest tests/test_verification_worker.py -q`

Expected: FAIL because `verification_worker.py` is absent.

- [x] **Step 3: Define verification policy V1**

For controlled Kubernetes actions, always verify target UID and desired operation result. If pre-action SLI evidence exists, also compare error rate and p95 latency over equal frozen windows. A target mismatch or worsened SLI is `regressed`; unreadable dependencies are `inconclusive`; a constant `pass=true` field is not accepted.

- [x] **Step 4: Make Verification inserts exact-idempotent**

Calculate `payload_hash` from action ID/hash, before/after evidence IDs, window and checks. Same verification ID/hash replays the stored row; a different hash returns `IDEMPOTENCY_KEY_REUSED`.

- [x] **Step 5: Implement the verification worker**

The worker leases Runs in `verifying`, obtains read-only observations through ToolRun/Evidence, appends Verification, then calls terminal commit. It never receives Action Executor credentials.

- [x] **Step 6: Append terminal events atomically**

Append `verification.completed` followed by `run.completed`; both use stable event IDs based on `verification_id`. Ensure the terminal commit checks lease epoch/token and expected Run state/version.

- [x] **Step 7: Run verification and state-machine tests**

Run: `cd ai-orchestrator && ./.venv312/bin/python -m pytest tests/test_verification_worker.py tests/test_investigation_runtime.py -q`

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/store -run 'Verification|Transition|TerminalCommit' -count=1`

Expected: PASS.

- [x] **Step 8: Commit verification closure**

```bash
git add ai-apm-query-go ai-orchestrator
git commit -m "feat: verify actions and close run terminal state"
```

---

### Task 7: Fix recovery ownership and aggregate idempotency

**Files:**
- Modify: `ai-apm-query-go/internal/store/ai_run_lease.go`
- Modify: `ai-apm-query-go/internal/api/control_plane_runs.go`
- Modify: `ai-apm-query-go/internal/store/ai_actions.go`
- Modify: `ai-apm-query-go/internal/store/ai_plan_steps.go`
- Modify: `ai-apm-query-go/internal/store/ai_hypotheses.go`
- Modify: `ai-apm-query-go/internal/store/ai_verifications.go`
- Modify: `ai-orchestrator/main.py`
- Test: `ai-apm-query-go/internal/store/ai_recovery_daos_test.go`
- Test: `ai-orchestrator/tests/test_investigation_recovery_startup.py`

**Interfaces:**
- Consumes: explicit worker kind `investigation|verification|action_reconcile`, status filters and `after_created_at/after_run_id` cursor.
- Produces: starvation-free recovery pages and exact idempotency semantics for every durable subaggregate.

- [x] **Step 1: Write a failing recovery starvation test**

Create 200 old `awaiting_approval` rows and one newer `investigating` row. Assert an Investigation scan returns the `investigating` row instead of an empty consumable page.

- [x] **Step 2: Run the recovery test**

Run: `cd ai-apm-query-go && go test ./internal/store -run RecoveryCandidate -count=1`

Expected: FAIL with the current all-nonterminal `LIMIT` query.

- [x] **Step 3: Replace implicit filtering with worker-owned status sets**

```text
investigation: created, planning, investigating
verification:  verifying
action_reconcile: executing where Action.execution_status=execution_unknown
waiting states: awaiting_confirmation, awaiting_approval; not worker candidates
```

Use keyset pagination `(created_at, run_id) > (?, ?)` and never filter returned rows again in Python.

- [x] **Step 4: Add payload hash comparisons to all Create methods**

On duplicate key: load the existing row in the same transaction. If hashes match, return the existing object and `replayed=true`; if hashes differ, return typed `ErrIdempotencyKeyReused`. Apply this to Action, Approval, PlanStep, Hypothesis and Verification.

- [x] **Step 5: Return existing projections to Orchestrator**

Internal append endpoints return `{created, replayed, payload_hash, resource}`. Orchestrator must use the returned stored resource rather than the newly generated in-memory payload after a replay.

- [x] **Step 6: Add recovery metrics**

Expose candidate count and oldest age by worker kind, lease-loss count, scan error count and replay/hash-conflict count. Alert when oldest actionable candidate exceeds twice the configured recovery interval.

- [x] **Step 7: Run recovery and idempotency suites**

Run: `cd ai-apm-query-go && go test ./internal/store ./internal/api -run 'Recovery|Idempotency|Hypothesis|PlanStep|Verification' -count=1`

Run: `cd ai-orchestrator && ./.venv312/bin/python -m pytest tests/test_investigation_recovery_startup.py tests/test_investigation_runtime.py -q`

Expected: PASS.

- [x] **Step 8: Commit recovery and replay fixes**

```bash
git add ai-apm-query-go ai-orchestrator
git commit -m "fix: make workflow recovery and aggregates deterministic"
```

---

### Task 8: Replace legacy approval UI and make Run/SSE projections truthful

**Files:**
- Create: `ai-apm-query-go/internal/api/run_projection.go`
- Create: `ai-apm-query-go/internal/api/run_projection_test.go`
- Modify: `ai-apm-query-go/internal/api/runs_public.go`
- Modify: `observability-frontend/package.json`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/pages/admin/Approvals.tsx`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx`
- Create: `observability-frontend/src/pages/admin/Approvals.test.tsx`
- Create: `observability-frontend/src/api/runEvents.test.ts`

**Interfaces:**
- Consumes: `GET /api/v1/ai/runs/{id}`, `GET /api/v1/ai/actions?status=...`, `POST /actions/{id}/decision`, Run SSE sequence IDs.
- Produces: typed `RunProjection`, canonical approval center and reconnecting SSE client.

- [x] **Step 1: Add a failing Run projection test**

```go
func TestRunProjectionDerivesRootCauseFromConfirmedHypothesis(t *testing.T) {
    got := ProjectRun(run, nil, []AIHypothesis{{Content: "DB saturation", Confidence: .86, ConfirmedByEvidence: true}}, nil, nil, nil)
    if got.RootCause != "DB saturation" || got.Confidence != .86 { t.Fatalf("projection=%#v", got) }
}
```

- [x] **Step 2: Implement a server-owned aggregate DTO**

Include Run identity/status, ordered plan steps, evidence summaries, hypotheses, derived root cause/confidence, latest Action/version, latest decision, latest Attempt, latest Verification and last event sequence. The frontend must not derive authority from transient graph node names.

- [x] **Step 3: Add canonical Action list and decision client methods**

```ts
export const listActions = (params?: { status?: string }) => api.get<ActionListResponse>('/ai/actions', { params })
export const decideAction = (id: string, body: ActionDecisionRequest) =>
  api.post<ActionDecisionResult>(`/ai/actions/${encodeURIComponent(id)}/decision`, body)
```

Remove `listApprovalTasks`, `approveTask` and `rejectTask` imports from `Approvals.tsx`.

- [x] **Step 4: Add frontend test tooling**

Add `vitest`, `jsdom`, `@testing-library/react` and `@testing-library/user-event` as dev dependencies and scripts `test` and `test:run`. Lockfile changes are committed with `package.json`.

- [x] **Step 5: Write approval UI tests**

Test that the page renders target UID/resourceVersion, action hash/version, canonical parameters, risk and proposer; approval sends no approver/hash; self-approval or stale-version 409 displays the server error; rejected Actions cannot execute.

- [x] **Step 6: Implement SSE resume and bounded reconnect**

Parse `id:` from every frame, retain the largest sequence, send `Last-Event-ID` on reconnect, retry with capped exponential backoff and jitter, and refetch the Run projection after reconnect or retention errors. Abort stops both the current fetch and retry timer.

- [x] **Step 7: Derive investigation details only from the projection**

Replace `r.root_cause ?? 'unknown'` fallbacks with the server projection. Display the durable plan, hypotheses, Action, approval, Attempt and Verification timeline.

- [x] **Step 8: Run frontend and API verification**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'RunProjection|ActionList' -count=1`

Run: `cd observability-frontend && npm run test:run`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

- [x] **Step 9: Commit the canonical UI cutover**

```bash
git add ai-apm-query-go observability-frontend
git commit -m "feat: switch approvals and investigation UI to canonical workflow"
```

---

### Task 9: Enforce LLM secret isolation and capability readiness

**Files:**
- Modify: `ai-apm-query-go/internal/api/settings.go`
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-llm-egress-proxy/main.go`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/values-prod.yaml`
- Modify: `deploy/helm/aiops/templates/networkpolicy.yaml`
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`
- Test: `ai-apm-query-go/internal/api/settings_test.go`
- Test: `ai-orchestrator/tests/test_readiness.py`
- Test: `ai-llm-egress-proxy/main_test.go`

**Interfaces:**
- Consumes: proxy URL/token and provider/model metadata.
- Produces: production Orchestrator with no Provider key, no direct LLM egress and readiness based on required Investigation capabilities.

- [x] **Step 1: Add tests proving the internal settings endpoint omits API keys**

Assert `/settings/llm/internal` never serializes `api_key` or `apiKey`, even when the stored encrypted key exists.

- [x] **Step 2: Add readiness dependency tests**

Test 503 for missing Query API, signing issuer, dispatcher/recovery heartbeat or production LLM proxy; test 200 only when all required Investigation dependencies pass.

- [x] **Step 3: Remove Orchestrator direct-key fallback**

`_fetch_saved_llm_config` must return `None` when `LLM_PROXY_URL` or `LLM_PROXY_TOKEN` is missing in production. Delete the branch that reads `cfg.api_key`. Keep local mock mode explicit through `LLM_MOCK=true`.

- [x] **Step 4: Restrict provider keys to the proxy Secret**

The proxy reads provider keys from its own Secret environment. Query API returns provider/model/base URL metadata only. Redact Authorization headers and request bodies from proxy logs.

- [x] **Step 5: Enable the proxy and orchestrator egress policy in production values**

Set `llmEgressProxy.enabled=true`; add `ai-orchestrator` to egress canary services; allow Orchestrator only to Query API, LLM proxy and DNS. The proxy alone receives external TCP 443 allowlist egress.

- [x] **Step 6: Implement capability readiness**

Return structured checks for `run_persistence`, `query_api`, `signing`, `recovery_worker`, `dispatch_queue`, `checkpoint_mode` and `llm_proxy`. Required check failures return 503; optional Chat/marketplace failures appear under `degraded_capabilities` without masking Investigation readiness.

- [x] **Step 7: Run service and Helm tests**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'LLMSettings|Ready' -count=1`

Run: `cd ai-orchestrator && ./.venv312/bin/python -m pytest tests/test_readiness.py -q`

Run: `cd ai-llm-egress-proxy && go test ./... -count=1`

Run: `helm lint deploy/helm/aiops -f deploy/helm/aiops/values-prod.yaml`

Expected: PASS.

- [x] **Step 8: Commit production isolation**

```bash
git add ai-apm-query-go ai-orchestrator ai-llm-egress-proxy deploy/helm/aiops
git commit -m "security: enforce llm proxy and capability readiness"
```

---

### Task 10: Isolate a stateless Investigation Worker

**Files:**
- Create: `ai-orchestrator/investigation_app.py`
- Create: `ai-orchestrator/tests/test_investigation_app.py`
- Modify: `ai-orchestrator/orchestrator.py`
- Modify: `ai-orchestrator/main.py`
- Create: `deploy/helm/aiops/templates/investigation-worker/deployment.yaml`
- Create: `deploy/helm/aiops/templates/investigation-worker/service.yaml`
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/values-prod.yaml`
- Modify: `ai-apm-query-go/internal/api/run_dispatch.go`

**Interfaces:**
- Consumes: signed Investigation invocation and MySQL-backed control-plane recovery.
- Produces: a stateless, multi-replica Investigation Worker without SQLite/Chroma PVC; the existing Orchestrator remains the Chat/legacy gateway during contract migration.

- [x] **Step 1: Add a failing test that Investigation mode never initializes SQLite**

```python
def test_investigation_app_has_no_local_checkpointer(monkeypatch):
    monkeypatch.setenv("ORCHESTRATOR_ROLE", "investigation")
    app = build_investigation_app()
    assert app.state.checkpoint_backend == "control-plane"
    assert not hasattr(app.state, "sqlite_connection")
```

- [x] **Step 2: Compile the Investigation graph without a local checkpointer**

The durable frontier is reconstructed from Run, PlanStep, ToolRun, Evidence, Action and Verification. Chat continues using its existing SQLite checkpointer until separated; canonical Investigation must never call `_ensure_async_checkpointer`.

- [x] **Step 3: Build a narrow FastAPI app**

Expose only `/internal/v1/run-invocations`, `/health`, `/readyz` and `/metrics`. Reuse signature validation, dispatcher and recovery modules. Do not register Chat, marketplace, legacy task, scheduler or session routes.

- [x] **Step 4: Add a dedicated Helm Deployment**

Use two replicas in production, no PVC, read-only ServiceAccount, no database Secret, and egress only to Query API, LLM proxy and DNS. Point Query API `ORCHESTRATOR_URL` for Investigation dispatch to the new Service.

- [x] **Step 5: Keep legacy Orchestrator single-replica but remove Investigation ownership**

It may retain Chat/SQLite temporarily. Reject `ai.investigate` on its public/internal ingress after the dispatch cutover, preventing two worker populations from owning the same Run.

- [x] **Step 6: Test two-worker lease competition**

Start two dispatcher instances against the same fake control plane and assert exactly one claims the Run while the other receives a fencing conflict and performs no Tool I/O.

- [x] **Step 7: Run Python and Helm verification**

Run: `cd ai-orchestrator && ./.venv312/bin/python -m pytest tests/test_investigation_app.py tests/test_investigation_dispatcher.py tests/test_investigation_recovery_startup.py -q`

Run: `helm template aiops deploy/helm/aiops -f deploy/helm/aiops/values-prod.yaml | rg 'investigation-worker|orchestrator-data'`

Expected: Investigation Worker resources are present and its pod spec has no `orchestrator-data` mount.

- [x] **Step 8: Commit worker isolation**

```bash
git add ai-orchestrator ai-apm-query-go deploy/helm/aiops
git commit -m "refactor: isolate stateless investigation workers"
```

---

### Task 11: Add cross-service contract and fault-injection gates

**Files:**
- Create: `tests/workflow-e2e/docker-compose.yml`
- Create: `tests/workflow-e2e/fake_executor.go`
- Create: `tests/workflow-e2e/fake_sources.py`
- Create: `tests/workflow-e2e/test_readonly_workflow.py`
- Create: `tests/workflow-e2e/test_action_workflow.py`
- Create: `tests/workflow-e2e/test_failure_recovery.py`
- Modify: `Makefile`
- Modify: `.github/workflows/aiops-workflow-gates.yml`

**Interfaces:**
- Consumes: real Query API handlers, disposable MySQL, Investigation Worker, fake deterministic data sources and fake signed Executor.
- Produces: executable G0-G5 release evidence.

- [x] **Step 1: Create a deterministic local topology**

Compose MySQL, schema migrator, Query API, Investigation Worker, fake data sources and fake Executor. Use fixed tenant/cluster IDs and generated ephemeral signing keys. No test connects to a real cluster or Provider LLM.

- [x] **Step 2: Add the read-only golden-path test**

Assert `create -> outbox -> accept -> planning -> investigating -> ToolRun -> Evidence -> Hypothesis -> verifying -> success -> SSE replay`, with one Run/invocation identity and monotonically increasing event sequence.

- [x] **Step 3: Add the controlled-action golden-path test**

Assert `awaiting_approval -> decision -> action outbox -> Attempt -> Executor -> verifying -> Verification -> success`; verify the approval identity and action hash/version at every boundary.

- [x] **Step 4: Add rejection and authorization tests**

Cover non-admin, self-approval, cross-tenant Action access, stale action version, changed payload with reused idempotency key and unsupported operation.

- [x] **Step 5: Add failure injection at every durable boundary**

Kill or drop responses after Run accept, ToolRun begin, data-source result, Evidence consume, approval commit, mutation apply, reconcile GET and terminal commit. Assert no duplicate Tool I/O, no duplicate mutation and eventual legal state.

- [x] **Step 6: Add SSE disconnect and retention tests**

Disconnect after a known sequence, reconnect with `Last-Event-ID`, and assert no gaps or duplicates. For a sequence older than retention, require a typed retention response and full projection refresh.

- [x] **Step 7: Add Make targets**

```make
test-workflow-contract:
	cd tests/workflow-e2e && python -m pytest -q

test-workflow-all:
	$(MAKE) test-workflow-contract
	cd ai-apm-query-go && go test ./...
	cd ai-orchestrator && ./.venv312/bin/python -m pytest tests -q
	cd observability-frontend && npm run test:run && npm run build
```

- [x] **Step 8: Run the full gate locally**

Run: `make test-workflow-all`

Expected: PASS with no skipped canonical workflow test. Environment-only real-cluster tests may remain opt-in, but the fake signed Executor path is mandatory in CI.

- [x] **Step 9: Commit release gates**

```bash
git add tests Makefile .github
git commit -m "test: add aiops workflow contract and fault gates"
```

---

### Task 12: Roll out safely and remove split-brain compatibility paths

**Files:**
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/values-prod.yaml`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/pages/admin/Approvals.tsx`
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-apm-query-go/internal/api/action_control.go`
- Modify: `docs/superpowers/specs/2026-08-25-aiops-workflow-convergence-design.md`
- Create: `docs/runbooks/aiops-action-rollout.md`

**Interfaces:**
- Consumes: passing Task 11 evidence and production metrics.
- Produces: staged activation, explicit rollback points and removal of legacy approval/mutation ownership.

- [ ] **Step 1: Deploy schema and code with mutation disabled**

Apply `0009`, deploy Query API and Workers with `CANONICAL_ACTION_DECISIONS=false`, `ACTION_DISPATCH_ENABLED=false`, `EXECUTION_MODE=disabled`, `realMutation=false`. Verify old read-only Runs remain compatible.

- [ ] **Step 2: Enable canonical Action generation and shadow projections**

Set `CANONICAL_ACTION_DECISIONS=true` while keeping dispatch disabled. Compare new Action/Approval projections with legacy UI telemetry for one release. No Action can cross Executor.

- [ ] **Step 3: Cut the frontend to canonical approvals**

Enable the new approval center for admins. Keep the legacy page read-only with a deprecation banner for one release; remove its approve/reject buttons.

- [ ] **Step 4: Enable dry execution against the fake/canary Executor**

Set `ACTION_DISPATCH_ENABLED=true` with Executor still `EXECUTION_MODE=disabled`; confirm rejected outcomes, Attempts, events, dashboards and alerts behave as designed.

- [ ] **Step 5: Run a single-target production canary**

After G0-G5 pass and a human change approval exists, enable `EXECUTION_MODE=approved` plus `realMutation=true` only for one namespace, one Deployment and `patch` operation. Keep `scale` disabled for the first canary. Observe outbox delay, Attempt count, reconcile backlog, verification outcome and Run terminal state.

- [x] **Step 6: Define automatic rollback triggers**

Immediately set `EXECUTION_MODE=disabled` and `ACTION_DISPATCH_ENABLED=false` when any of these occurs: duplicate Attempt/mutation, Action hash mismatch, unresolved execution older than two reconcile intervals, verification regression, cross-tenant denial, lease fencing failure or missing audit event. Schema remains in place during rollback.

- [ ] **Step 7: Remove legacy write paths after one stable release**

Return HTTP 410 for `/ops/tasks/{id}/approve`, `/ops/tasks/{id}/reject` and direct synchronous `/ai/actions/{id}/execute`; remove Orchestrator direct shell/Kubernetes execution code and its feature flags. Keep historical legacy records read-only.

- [ ] **Step 8: Update the design and runbook with actual evidence**

Record exact deployed image tags, migration version, feature flags, G0-G5 test command outputs, canary target, start/end time, rollback decision and known limitations. Do not mark controlled action complete without a real reconcile and independent verification record.

- [x] **Step 9: Run final verification and commit cleanup**

Run: `make test-workflow-all`

Run: `helm lint deploy/helm/aiops -f deploy/helm/aiops/values-prod.yaml`

Run: `git status --short && git diff --check`

Expected: all tests pass, Helm lint passes, no whitespace errors, and only intended files are modified.

```bash
git add deploy observability-frontend ai-orchestrator ai-apm-query-go docs
git commit -m "refactor: retire legacy aiops mutation workflows"
```

---

## Release Gates

## Execution closure (2026-08-25)

本轮已在当前工作区完成并验证 Task 4-11 的代码闭环：durable Action outbox/lease
fencing、确定性 Attempt、真实签名 reconcile 四态、独立 Verification、worker-owned
recovery、canonical Run/Action 投影、LLM proxy 隔离、stateless Investigation Worker、
跨服务故障契约和发布门禁均已落盘。最终命令
`./deploy/scripts/verify-aiops-workflow-gates.sh` 全部通过（Go、4 个 workflow contract、
Python 1167 passed/1 skipped、Executor、前端 4 tests passed + build、Helm lint/render/RBAC）。

Task 8 Step 4-5 已在本轮补齐：Vitest、jsdom、Testing Library、审批 UI 测试和 SSE
断线恢复测试已提交并通过。当前唯一未勾选的是 Task 12 的真实发布条件：

- Task 12 Step 5：真实集群单目标 canary 需要人工变更审批、真实目标 namespace 和签名
  Executor，当前环境未执行；生产仍保持 `EXECUTION_MODE=disabled`、`realMutation=false`。
- Task 12 Step 1-4、7-8 属于实际部署/稳定发布后的运行步骤，必须在 Step 5 完成后按
  runbook 记录真实镜像、目标、时间窗口、回滚结论和旧写路径移除证据，不能用本地测试替代。

- **G0 — Action identity:** canonical payload hash includes target UID/resourceVersion, operation, normalized params and policy version; stale approval and mismatched replay return 409.
- **G1 — Approval authority:** approver comes from JWT/MySQL role truth; self-approval and cross-tenant decisions are rejected; approval and Action outbox are atomic.
- **G2 — Mutation exactly-once effect:** response loss creates `execution_unknown`; the same immutable Action never produces a second Executor mutation call.
- **G3 — Real reconciliation:** applied/not_applied/drift/unknown come from real target reads; no code path emits synthetic `reconciled`.
- **G4 — Verification closure:** successful execution always enters verifying; terminal success requires an independent passed Verification; regressions become `regressed` or `rollback_required`.
- **G5 — Recovery and UI truth:** worker death at every boundary eventually converges without duplicate side effects; Run projection and SSE can reconstruct the same durable timeline.

## Rollback Model

- Migration `0009` is expand-only; application rollback leaves new columns/tables unused and does not drop data.
- Feature activation order is `canonical decisions -> UI -> action dispatch -> executor approved -> realMutation`; rollback reverses this order.
- Turning off Action dispatch never changes existing `execution_unknown` to failed or success; reconciler remains enabled until every in-flight Action reaches a proven state.
- Investigation read-only execution remains available if mutation is disabled; LLM proxy or Query API dependency failure makes readiness fail closed rather than falling back to direct credentials.

## Effort and Parallelism

- Tasks 1-3: 7-10 engineering days; they are serial because schema and Action V2 define all later contracts.
- Tasks 4-7: 10-15 engineering days; dispatch/reconcile and verification/recovery can proceed in two workstreams after Task 3.
- Tasks 8-10: 8-12 engineering days; frontend and production isolation can proceed in parallel after API contracts stabilize.
- Tasks 11-12: 6-10 engineering days plus the required canary observation period.
- One engineer: approximately 31-47 engineering days. Two coordinated engineers: approximately 4-6 calendar weeks, excluding production change approval and canary observation.

## Plan Self-Review Result

- Spec coverage: Run authority, Tool/Evidence boundary, approval, Action Executor, verification, recovery, SSE, frontend truth and controlled-action gates all map to Tasks 1-12.
- Type consistency: Action V2 uses `action_version`, `hash_schema_version`, `action_hash`, `policy_version`; the same fields flow through Decision, Outbox, Attempt, Executor and Reconcile.
- Safety consistency: no task enables real mutation before G0-G5; every unknown write outcome stays unknown until a real target observation resolves it.
- Scope boundary: Chat checkpoint migration is not required for canonical Investigation HA; Task 10 isolates Investigation first and leaves Chat/legacy SQLite as a separately removable compatibility service.
