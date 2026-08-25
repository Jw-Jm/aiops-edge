package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testSigner 是 D-Gate 签名测试用的 Ed25519 密钥对（模拟 query-api 签发）。
type testKeypair struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

var testSigner = func() testKeypair {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return testKeypair{priv: priv, pub: pub}
}()

func newTestServer(mode ExecutionMode) *server {
	return &server{
		mode: mode, results: map[string]ActionResult{},
		verifyKeyB64: base64.RawURLEncoding.EncodeToString(testSigner.pub),
	}
}

func execBody() ActionExecutionContext {
	return ActionExecutionContext{
		ActionID: "act-1", ActionHash: "h1", TargetUID: "uid-1", ResourceVersion: "rv-1",
		Operation: "patch", CredentialRef: "ref-1", ApprovedAt: "2026-01-01T00:00:00Z",
	}
}

// signedPOST 构造带 Ed25519 签名的执行请求（D-Gate：必须已签名）。
func signedPOST(path string, body interface{}) (*httptest.ResponseRecorder, *http.Request) {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	digest := sha256.Sum256(b)
	sig := ed25519.Sign(testSigner.priv, digest[:])
	req.Header.Set("X-Executor-Signature", hex.EncodeToString(sig))
	rec := httptest.NewRecorder()
	return rec, req
}

func doSigned(h http.Handler, path string, body interface{}) *httptest.ResponseRecorder {
	rec, req := signedPOST(path, body)
	h.ServeHTTP(rec, req)
	return rec
}

func postJSON(h http.Handler, path string, body interface{}) *httptest.ResponseRecorder {
	return doSigned(h, path, body)
}

func TestDisabledModeRejectsExecution(t *testing.T) {
	s := newTestServer(ModeDisabled)
	rec := postJSON(http.HandlerFunc(s.handleExecute), "/v1/executor/execute", execBody())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled mode should reject, got %d", rec.Code)
	}
	var res ActionResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Status != "rejected" {
		t.Fatalf("expected rejected, got %s", res.Status)
	}
}

func TestMissingActionHashRejected(t *testing.T) {
	s := newTestServer(ModeApproved)
	b := execBody()
	b.ActionHash = ""
	rec := postJSON(http.HandlerFunc(s.handleExecute), "/v1/executor/execute", b)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing action_hash, got %d", rec.Code)
	}
}

func TestApprovedModeExecutesWithCleanTOCTOU(t *testing.T) {
	s := newTestServer(ModeApproved)
	rec := postJSON(http.HandlerFunc(s.handleExecute), "/v1/executor/execute", execBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res ActionResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Status != "success" {
		t.Fatalf("expected success, got %s", res.Status)
	}
	// status endpoint
	rec2 := httptest.NewRecorder()
	s.handleStatus(rec2, httptest.NewRequest(http.MethodGet, "/v1/executor/status/act-1", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("status endpoint failed: %d", rec2.Code)
	}
}

func TestApprovedModeRejectsUnsignedContext(t *testing.T) {
	// D-Gate：approved 模式必须已签名执行上下文（不可仅靠共享 token 走真实路径）。
	s := newTestServer(ModeApproved)
	body, _ := json.Marshal(execBody())
	req := httptest.NewRequest(http.MethodPost, "/v1/executor/execute", bytes.NewReader(body))
	// 无 X-Executor-Signature → 拒绝
	rec := httptest.NewRecorder()
	s.handleExecute(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unsigned context in approved mode should be rejected, got %d", rec.Code)
	}
	var res ActionResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !strings.Contains(res.Message, "signed execution context") {
		t.Fatalf("message should mention signed context, got: %s", res.Message)
	}
}

func TestApprovedModeDoesNotImplyRealExecution(t *testing.T) {
	// F5 已废除：approved 模式若无 POD_SA_ACCESS（k8sEnabled=false）→ 明确"mutation NOT applied"，
	// 不伪装成真实动作已执行；approved + k8sEnabled 才执行真实 patch（本单测不启用 k8s）。
	s := newTestServer(ModeApproved)
	rec := postJSON(http.HandlerFunc(s.handleExecute), "/v1/executor/execute", execBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var res ActionResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Status != "success" {
		t.Fatalf("expected success (recorded), got %s", res.Status)
	}
	// k8sEnabled=false → 明确 NOT applied（不得伪装成真实 mutation）。
	if !strings.Contains(res.Message, "mutation NOT applied") {
		t.Fatalf("message must state mutation not applied without SA access, got: %s", res.Message)
	}
}

func TestReconcileNeverBlindRetry(t *testing.T) {
	s := newTestServer(ModeApproved)
	rec := postJSON(http.HandlerFunc(s.handleReconcile), "/v1/executor/reconcile",
		map[string]string{"action_id": "act-x", "target_uid": "uid-x", "expected_spec": "s"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile failed: %d", rec.Code)
	}
	var res ActionResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	// 必须含 reconcile-before-retry 语义（禁止对未知写操作盲目 retry）。
	if res.Status != "reconciled" {
		t.Fatalf("expected reconciled, got %s", res.Status)
	}
	if len(res.Message) == 0 {
		t.Fatalf("reconcile must return message")
	}
}
