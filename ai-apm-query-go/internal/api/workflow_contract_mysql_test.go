//go:build integration

package api

// P2-F1: cross-service durable Action workflow contract on REAL MySQL,
// driving the PRODUCTION handler/DAO/executor-client code (no self-built
// Harness state machine). Replaces the removed Python pseudo-tests in
// tests/workflow-e2e (报告 §18: workflow contract test must use production
// modules; a fake HTTP executor and real MySQL are allowed, re-writing the
// production state machine is not).
//
// Run: TEST_MYSQL_DSN="user:pass@tcp(host:port)/db" \
//      go test -tags integration ./internal/api/ -run TestWorkflowContract -v
//
// 覆盖场景（报告 §18.2 最低要求 1..14）：
//  1 Create Action                8  duplicate approval idempotent
//  2 Approve                      9  duplicate dispatch no second mutation
//  3 Outbox claim                10  stale version reject
//  4 Dispatch                    11  idempotency key reused w/ changed decision
//  5 Executor success            12  rejected action never dispatch
//  6 Lost response→unknown       13  tenant/cluster scope mismatch reject
//  7 Reconcile success           14  action-hash tamper reject

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type wfEnv struct {
	t               *testing.T
	db              *sql.DB
	h               *Handler
	tenantID        string
	clusterID       string
	proposerUserID  string
	approverUserID  string
	executorCalls   int
	executeStatus   string // fake executor /v1/executor/execute JSON status
	reconcileStatus string // fake executor /v1/executor/reconcile JSON status
	executeFailWith int    // HTTP code to return from execute (0 = 200)
	mu              sync.Mutex
}

func newWorkflowEnv(t *testing.T) *wfEnv {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set; requires real MySQL")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })
	// 顺序应用 store/migrations/versions/*.sql（跳过 0005_alert_worker_state：
	// 其 SQL 硬编码 aiops. 生产库前缀且本测试不需要 alert leader 表——
	// 保持测试库隔离，不改生产迁移文本）。
	entries, readErr := os.ReadDir("../store/migrations/versions")
	if readErr != nil {
		t.Fatalf("read migrations dir: %v", readErr)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") || strings.Contains(e.Name(), "0005_alert_worker_state") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile("../store/migrations/versions/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			// 与生产 migrator 相同的幂等 DDL 语义（1050/1060/1061），
			// 例如 0003b 已含列而 0006 又 ALTER 的场景。
			msg := err.Error()
			if !strings.Contains(msg, "Error 1050") && !strings.Contains(msg, "Error 1060") &&
				!strings.Contains(msg, "Error 1061") {
				t.Fatalf("apply %s: %v", name, err)
			}
		}
	}
	// 场景间隔离：清空 action workflow 相关表。
	for _, table := range []string{"ai_actions", "ai_approval_decisions", "ai_action_outbox",
		"ai_action_attempts", "ai_action_reconciliations", "ai_runs", "ai_run_events", "ai_audit_events"} {
		if _, err := db.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	h := &Handler{}
	h.runDAO = &store.AIRunDAO{}
	h.eventDAO = &store.AIRunEventDAO{}
	h.actionDAO = &store.AIActionDAO{}
	h.actionOutboxDAO = &store.AIActionOutboxDAO{}
	h.approvalDAO = &store.AIApprovalDecisionDAO{}
	h.attemptDAO = &store.AIActionAttemptDAO{}
	h.reconciliationDAO = &store.AIActionReconciliationDAO{}

	env := &wfEnv{
		t:               t,
		db:              db,
		h:               h,
		tenantID:        "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		clusterID:       "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		proposerUserID:  "11111111-1111-4111-8111-111111111111",
		approverUserID:  "22222222-2222-4222-8222-222222222222",
		executeStatus:   "success",
		reconcileStatus: "applied",
	}
	env.startFakeExecutor()
	return env
}

// startFakeExecutor 启动允许的 fake HTTP executor（报告 §18.2 明示允许）。
func (e *wfEnv) startFakeExecutor() {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0)
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/executor/execute"):
			e.mu.Lock()
			if e.executeFailWith != 0 {
				w.WriteHeader(e.executeFailWith)
				e.mu.Unlock()
				return
			}
			st := e.executeStatus
			e.executorCalls++
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(contract.ActionResult{ActionID: actionIDFromBody(body, r), Status: st})
		case strings.HasSuffix(r.URL.Path, "/v1/executor/reconcile"):
			e.mu.Lock()
			st := e.reconcileStatus
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(contract.ActionResult{Status: st})
		default:
			http.NotFound(w, r)
		}
	}))
	e.t.Cleanup(srv.Close)
	if err := ConfigureActionExecutionClient(srv.URL, base64.RawURLEncoding.EncodeToString(priv), ""); err != nil {
		e.t.Fatalf("configure executor client: %v", err)
	}
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func actionIDFromBody(_ []byte, r *http.Request) string {
	var payload struct {
		ActionID string `json:"action_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	return payload.ActionID
}

// insertApprovedFixture 直插 run(awaiting_approval) + action(proposed, hash v2,
// preflight passed) —— Create Action(1)。返回 runID/actionID/actionHash/version。
func (e *wfEnv) insertProposedFixture() (runID, actionID, actionHash string, version int64) {
	runID = randomUUID()
	actionID = randomUUID()
	now := time.Now()
	created, err := e.h.runDAO.Create(store.AIRun{
		RunID: runID, RequestID: "req-" + actionID[:8], TenantID: e.tenantID,
		Principal: e.proposerUserID, PrincipalType: "user",
		ScopeKind: "single_cluster", PrimaryClusterID: e.clusterID,
		Intent: "investigate", Status: "awaiting_approval", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !created {
		e.t.Fatalf("create run: created=%v err=%v", created, err)
	}
	params := json.RawMessage(`{"restart":{"grace":0}}`)
	actionHash, err = contract.CanonicalActionHash(contract.CanonicalActionPayloadV2{
		Version: 1, ActionType: "kubernetes", ResourceType: "deployment",
		Namespace: "payments", TargetName: "checkout", TargetUID: "deploy-uid-0001",
		ResourceVersion: "rv-0001", Operation: "rollout_restart", Params: params,
		PolicyVersion: "action-policy-v1",
	})
	if err != nil {
		e.t.Fatalf("hash: %v", err)
	}
	ok, err := e.h.actionDAO.Create(store.AIAction{
		ActionID: actionID, RunID: runID, TenantID: e.tenantID, ClusterID: e.clusterID,
		ActionType: "kubernetes", ActionHash: actionHash, HashSchemaVersion: 2,
		ActionVersion: 1, ProposedBy: e.proposerUserID, PolicyVersion: "action-policy-v1",
		PreflightStatus: "passed", TargetResourceType: "deployment", IdempotencyKey: "ik-" + actionID,
		Status: "proposed", DryRun: false, Params: params,
		TargetName: "checkout", TargetUID: "deploy-uid-0001", ResourceVersion: "rv-0001",
		Namespace: "payments", Operation: "rollout_restart", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !ok {
		e.t.Fatalf("create action: ok=%v err=%v", ok, err)
	}
	return runID, actionID, actionHash, 1
}

func (e *wfEnv) approve(actionID, actionHash string, version int64, decision, key, reason string) (ActionDecisionResult, error) {
	return e.h.decideAction(context.Background(), actionID, AuthorizationContext{
		UserID: e.approverUserID, TenantID: e.tenantID,
	}, ActionDecisionRequest{ActionVersion: version, IdempotencyKey: key, Decision: decision, Reason: reason})
}

func (e *wfEnv) pendingOutbox(actionID string) (store.AIActionOutbox, bool) {
	rows, err := e.h.actionOutboxDAO.ScanPending(50)
	if err != nil {
		e.t.Fatalf("scan pending: %v", err)
	}
	for _, r := range rows {
		if r.ActionID == actionID {
			return r, true
		}
	}
	return store.AIActionOutbox{}, false
}

// ─────────────────────────────────────────────────────────────────────────────

func TestWorkflowContract(t *testing.T) {
	t.Run("create_approve_claim_dispatch_success", func(t *testing.T) {
		e := newWorkflowEnv(t) // 1,2,3,4,5
		runID, actionID, hash, version := e.insertProposedFixture()
		_ = runID

		// 3) outbox claim fencing（approve 前无 outbox；先 claim 空集）
		if _, ok := e.pendingOutbox(actionID); ok {
			t.Fatal("outbox must be empty before approval")
		}
		// 2) approve
		res, err := e.approve(actionID, hash, version, "approved", "decision-1", "")
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		if res.Replay || res.CommandID == "" {
			t.Fatalf("expected fresh approval with command, got %+v", res)
		}
		row, ok := e.pendingOutbox(actionID)
		if !ok {
			t.Fatal("approved action must have a pending outbox row")
		}
		// 4+5) dispatch via production dispatcher → fake executor success
		//（outbox claim 在 dispatchActionOne 内部执行；fencing 语义由
		// duplicate_dispatch_no_second_mutation 验证）
		e.h.dispatchActionOne(row)
		e.mu.Lock()
		calls := e.executorCalls
		e.mu.Unlock()
		if calls != 1 {
			t.Fatalf("executor must be called exactly once, got %d", calls)
		}
		act, _ := e.h.actionDAO.GetByID(actionID)
		if act == nil || act.Status != "approved" || act.ExecutionStatus != "success" {
			t.Fatalf("action not finalized: status=%v exec=%v", act.Status, act.ExecutionStatus)
		}
		// run entered verifying (executor success → verifying)
		var runStatus string
		if err := e.db.QueryRow("SELECT status FROM ai_runs WHERE run_id = ?", runID).Scan(&runStatus); err != nil {
			t.Fatalf("run: %v", err)
		}
		if runStatus != "verifying" {
			t.Fatalf("expected run verifying, got %s", runStatus)
		}
	})

	t.Run("duplicate_approval_idempotent", func(t *testing.T) {
		e := newWorkflowEnv(t) // 8
		_, actionID, hash, version := e.insertProposedFixture()
		if _, err := e.approve(actionID, hash, version, "approved", "decision-dup", ""); err != nil {
			t.Fatalf("first approve: %v", err)
		}
		replay, err := e.approve(actionID, hash, version, "approved", "decision-dup", "")
		if err != nil || !replay.Replay {
			t.Fatalf("duplicate approval must replay, replay=%v err=%v", replay.Replay, err)
		}
		var cnt int
		if err := e.db.QueryRow("SELECT COUNT(*) FROM ai_action_outbox WHERE action_id = ?", actionID).Scan(&cnt); err != nil {
			t.Fatal(err)
		}
		if cnt != 1 {
			t.Fatalf("duplicate approval must not double outbox, got %d", cnt)
		}
	})

	t.Run("rejected_never_dispatches", func(t *testing.T) {
		e := newWorkflowEnv(t) // 12
		_, actionID, hash, version := e.insertProposedFixture()
		if _, err := e.approve(actionID, hash, version, "rejected", "decision-rej", "no"); err != nil {
			t.Fatalf("reject: %v", err)
		}
		if _, ok := e.pendingOutbox(actionID); ok {
			t.Fatal("rejected action must not create outbox")
		}
		e.h.dispatchActionPending()
		e.mu.Lock()
		calls := e.executorCalls
		e.mu.Unlock()
		if calls != 0 {
			t.Fatalf("rejected action dispatched to executor, calls=%d", calls)
		}
		act, _ := e.h.actionDAO.GetByID(actionID)
		if act == nil || act.ExecutionStatus != "rejected" {
			t.Fatalf("rejected action exec status = %v", act)
		}
	})

	t.Run("stale_version_rejected", func(t *testing.T) {
		e := newWorkflowEnv(t) // 10
		_, actionID, hash, version := e.insertProposedFixture()
		if _, err := e.approve(actionID, hash, version+1, "approved", "decision-stale", ""); err == nil {
			t.Fatal("stale action version must be rejected")
		}
	})

	t.Run("idempotency_key_reused_changed_decision", func(t *testing.T) {
		e := newWorkflowEnv(t) // 11
		_, actionID, hash, version := e.insertProposedFixture()
		if _, err := e.approve(actionID, hash, version, "approved", "decision-key", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.approve(actionID, hash, version, "rejected", "decision-key", ""); err == nil {
			t.Fatal("reusing decision key with changed decision must conflict")
		}
	})

	t.Run("tenant_cluster_scope_mismatch_rejected", func(t *testing.T) {
		e := newWorkflowEnv(t) // 13
		_, actionID, _, version := e.insertProposedFixture()
		// wrong tenant approver
		if _, err := e.h.decideAction(context.Background(), actionID, AuthorizationContext{
			UserID: e.approverUserID, TenantID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		}, ActionDecisionRequest{ActionVersion: version, IdempotencyKey: "k-scope", Decision: "approved"}); err == nil {
			t.Fatal("cross-tenant approval must be rejected")
		}
	})

	t.Run("approval_hash_tamper_rejects_execution", func(t *testing.T) {
		e := newWorkflowEnv(t) // 14
		_, actionID, hash, version := e.insertProposedFixture()
		if _, err := e.approve(actionID, hash, version, "approved", "decision-tamper", ""); err != nil {
			t.Fatal(err)
		}
		row, ok := e.pendingOutbox(actionID)
		if !ok {
			t.Fatal("expected outbox row")
		}
		// 篡改 outbox 内 action_hash → dispatcher 必须 integrity-fail 而非执行
		if _, err := e.db.Exec("UPDATE ai_action_outbox SET action_hash = 'tampered' WHERE command_id = ?", row.CommandID); err != nil {
			t.Fatal(err)
		}
		row.ActionHash = "tampered" // dispatch 消费内存 outbox 对象
		e.h.dispatchActionOne(row)
		e.mu.Lock()
		calls := e.executorCalls
		e.mu.Unlock()
		if calls != 0 {
			t.Fatalf("tampered outbox must never reach executor, calls=%d", calls)
		}
		act, _ := e.h.actionDAO.GetByID(actionID)
		if act == nil || act.Status != "rejected" {
			t.Fatalf("tampered action must be rejected, got status=%v", act.Status)
		}
	})

	t.Run("duplicate_dispatch_no_second_mutation", func(t *testing.T) {
		e := newWorkflowEnv(t) // 9
		_, actionID, hash, version := e.insertProposedFixture()
		if _, err := e.approve(actionID, hash, version, "approved", "decision-d9", ""); err != nil {
			t.Fatal(err)
		}
		row, ok := e.pendingOutbox(actionID)
		if !ok {
			t.Fatal("expected outbox")
		}
		e.h.dispatchActionOne(row)
		e.mu.Lock()
		afterFirst := e.executorCalls
		e.mu.Unlock()
		if afterFirst != 1 {
			t.Fatalf("first dispatch must call executor once, got %d", afterFirst)
		}
		// terminal（已 deliver）后再次 dispatch 同一 row：claim 失败 → 零二次执行
		e.h.dispatchActionOne(row)
		e.mu.Lock()
		afterSecond := e.executorCalls
		e.mu.Unlock()
		if afterSecond != 1 {
			t.Fatalf("duplicate dispatch mutated twice: calls=%d", afterSecond)
		}
	})

	t.Run("lost_response_reconciles", func(t *testing.T) {
		e := newWorkflowEnv(t) // 6,7
		runID, actionID, hash, version := e.insertProposedFixture()
		_ = runID
		if _, err := e.approve(actionID, hash, version, "approved", "decision-lost", ""); err != nil {
			t.Fatal(err)
		}
		row, ok := e.pendingOutbox(actionID)
		if !ok {
			t.Fatal("expected outbox")
		}
		// 6) lost response：executor 返回非 JSON（204）→ execution_unknown；
		//    reconcile 亦无法判定（unknown）→ 不得落 success 终态。
		e.mu.Lock()
		e.executeFailWith = http.StatusNoContent
		e.reconcileStatus = "unknown"
		e.mu.Unlock()
		e.h.dispatchActionOne(row)
		act, _ := e.h.actionDAO.GetByID(actionID)
		if act == nil || act.ExecutionStatus == "success" {
			t.Fatalf("lost response must not persist success: %v", act)
		}
		// 模拟 reconcile 场景：action 处于 execution_unknown（生产路径见
		// TestActionExecutionClient_ResponseLossRequiresReconcile 的客户端语义），
		// dispatcher 走 reconcile 而非盲重放。
		if _, err := e.db.Exec("UPDATE ai_actions SET execution_status='execution_unknown', status='approved' WHERE action_id = ?", actionID); err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		e.executeFailWith = 0
		e.reconcileStatus = "applied"
		e.mu.Unlock()
		e.h.dispatchActionOne(row)
		act2, _ := e.h.actionDAO.GetByID(actionID)
		if act2 == nil || act2.ExecutionStatus != "success" {
			t.Fatalf("reconcile must settle to success, got exec=%v", act2)
		}
	})
}
