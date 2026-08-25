package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// makeExecutorKeypair 生成测试用 Ed25519 密钥对（query-api 私钥 + executor 验签公钥）。
func makeExecutorKeypair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// verifyExecutorSig 模拟 ai-action-executor 的 verifySignedContext：X-Executor-Signature
// = Ed25519 over body SHA256，公钥 base64 RawURL。
func verifyExecutorSig(t *testing.T, priv ed25519.PrivateKey, r *http.Request, body []byte) bool {
	t.Helper()
	sigHex := r.Header.Get("X-Executor-Signature")
	if sigHex == "" {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	pub := priv.Public().(ed25519.PublicKey)
	digest := sha256.Sum256(body)
	return ed25519.Verify(pub, digest[:], sig)
}

func TestActionExecutionClient_SignatureMatchesExecutor(t *testing.T) {
	// 验证 query-api 签发签名能被 executor 的 verifySignedContext 逻辑验签通过（机制对齐）。
	priv, _ := makeExecutorKeypair(t)
	encoded := base64.RawURLEncoding.EncodeToString(priv)
	var received bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		received = verifyExecutorSig(t, priv, r, buf)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"action_id":"act-1","status":"success"}`))
	}))
	defer srv.Close()

	if err := ConfigureActionExecutionClient(srv.URL, encoded, ""); err != nil {
		t.Fatalf("configure: %v", err)
	}
	ctx := contract.ActionExecutionContext{
		ActionID: "act-1", ActionHash: "h1", ApprovalID: "ap-1",
		TargetUID: "uid-1", TargetName: "target", ResourceVersion: "rv-1",
		ClusterID: "c1", Namespace: "ns", Operation: "patch",
	}
	client := currentActionExecutor()
	if client == nil {
		t.Fatal("client not configured")
	}
	res, reached, err := client.Execute(ctx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !reached || res.Status != "success" {
		t.Fatalf("unexpected result reached=%v res=%+v", reached, res)
	}
	if !received {
		t.Fatal("executor verifySignedContext failed to validate query-api signature")
	}
}

func TestActionExecutionClient_ResponseLossRequiresReconcile(t *testing.T) {
	priv, _ := makeExecutorKeypair(t)
	encoded := base64.RawURLEncoding.EncodeToString(priv)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/v1/executor/execute" {
			// An empty/non-JSON response is not evidence that the mutation did not
			// happen; the client must surface execution_unknown.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/v1/executor/reconcile" {
			t.Fatalf("unexpected executor path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"action_id":"act-1","status":"applied","message":"already applied"}`))
	}))
	defer srv.Close()
	if err := ConfigureActionExecutionClient(srv.URL, encoded, ""); err != nil {
		t.Fatal(err)
	}
	client := currentActionExecutor()
	ctx := contract.ActionExecutionContext{ActionID: "act-1", ActionHash: "h1", ApprovalID: "ap-1",
		TargetUID: "uid-1", TargetName: "target", ResourceVersion: "rv-1", ClusterID: "c1",
		Namespace: "aiops-canary", Operation: "patch", TargetSpec: []byte(`{"metadata":{"annotations":{"aiops.observability.io/validation":"1"}}}`)}
	res, reached, err := client.Execute(ctx)
	if err != nil || !reached || res.Status != "execution_unknown" {
		t.Fatalf("response loss must become execution_unknown, reached=%v res=%+v err=%v", reached, res, err)
	}
	reconciled, reached, err := client.Reconcile(ctx)
	if err != nil || !reached || reconciled.Status != "applied" {
		t.Fatalf("reconcile result=%+v reached=%v err=%v", reconciled, reached, err)
	}
	if requests != 2 {
		t.Fatalf("expected one execute and one reconcile request, got %d", requests)
	}
}

func TestExecuteApprovedAction_Success_Persists(t *testing.T) {
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// UpdateExecution 会被调用（executor 返回 success）
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_actions SET execution_status")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	priv, _ := makeExecutorKeypair(t)
	encoded := base64.RawURLEncoding.EncodeToString(priv)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"action_id":"act-1","status":"success","message":"ok"}`))
	}))
	defer srv.Close()
	if err := ConfigureActionExecutionClient(srv.URL, encoded, ""); err != nil {
		t.Fatal(err)
	}

	h := &Handler{actionDAO: &store.AIActionDAO{}}
	approval := &store.AIApprovalDecision{ApprovalID: "ap-1", DecidedAt: &time.Time{}}
	action := &store.AIAction{
		ActionID: "act-1", ActionHash: "h1", ClusterID: "c1",
		TargetUID: "uid-1", TargetName: "target", ResourceVersion: "rv-1",
		Namespace: "ns", Operation: "patch",
		ExecutionStatus: "approved", DryRun: false,
		Result: []byte(`{}`),
	}
	res, err := h.executeApprovedAction(action, approval)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("expected success, got %s", res.Status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("UpdateExecution not called: %v", err)
	}
}

func TestExecuteApprovedAction_Disabled_Rejected(t *testing.T) {
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_actions SET execution_status")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	priv, _ := makeExecutorKeypair(t)
	encoded := base64.RawURLEncoding.EncodeToString(priv)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":"rejected","message":"EXECUTION_MODE=disabled; real mutation not permitted"}`))
	}))
	defer srv.Close()
	if err := ConfigureActionExecutionClient(srv.URL, encoded, ""); err != nil {
		t.Fatal(err)
	}

	h := &Handler{actionDAO: &store.AIActionDAO{}}
	action := &store.AIAction{
		ActionID: "act-1", ActionHash: "h1", ClusterID: "c1",
		TargetUID: "uid-1", TargetName: "target", Namespace: "ns", Operation: "patch",
		ExecutionStatus: "approved", DryRun: false,
	}
	approval := &store.AIApprovalDecision{ApprovalID: "ap-1", DecidedAt: &time.Time{}}
	res, err := h.executeApprovedAction(action, approval)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Status != "rejected" {
		t.Fatalf("expected rejected (disabled), got %s", res.Status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("UpdateExecution not called: %v", err)
	}
}

func TestExecuteApprovedAction_TerminalIdempotent(t *testing.T) {
	// durable idempotency：execution_status 已 terminal → 直接返回已记录结果，不调 executor/DB。
	h := &Handler{actionDAO: &store.AIActionDAO{}}
	action := &store.AIAction{
		ActionID: "act-1", ExecutionStatus: "success", DryRun: false,
		Result: []byte(`{"action_id":"act-1","status":"success"}`),
	}
	approval := &store.AIApprovalDecision{ApprovalID: "ap-1", DecidedAt: &time.Time{}}
	res, err := h.executeApprovedAction(action, approval)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("expected terminal success, got %s", res.Status)
	}
}

func TestExecuteApprovedAction_NotApproved_Gate(t *testing.T) {
	// handler 层门禁：无 approved 审批 → ACTION_NOT_APPROVED（不触发执行）。
	// 通过 executeApprovedAction 不校验 approval（校验在 handler），此处仅验证 dry_run 拒绝。
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	h := &Handler{actionDAO: &store.AIActionDAO{}}
	action := &store.AIAction{
		ActionID: "act-1", ExecutionStatus: "approved", DryRun: true,
	}
	approval := &store.AIApprovalDecision{ApprovalID: "ap-1", DecidedAt: &time.Time{}}
	_, err := h.executeApprovedAction(action, approval)
	if err == nil {
		t.Fatal("expected error for dry_run action via executor")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}
