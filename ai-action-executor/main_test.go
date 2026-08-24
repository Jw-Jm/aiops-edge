package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(mode ExecutionMode) *server {
	return &server{mode: mode, results: map[string]ActionResult{}}
}

func execBody() ActionExecutionContext {
	return ActionExecutionContext{
		ActionID: "act-1", ActionHash: "h1", TargetUID: "uid-1", ResourceVersion: "rv-1",
		Operation: "patch", CredentialRef: "ref-1", ApprovedAt: "2026-01-01T00:00:00Z",
	}
}

func postJSON(h http.Handler, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
