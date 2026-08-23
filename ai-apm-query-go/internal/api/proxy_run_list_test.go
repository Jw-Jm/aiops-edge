package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// TestProxyAIRunListForwardsToOrchestrator 验证 GET /api/v1/ai/runs 经 ProxyAI 转发到
// orchestrator 并注入方向凭据 X-Internal-Token（P12 Run API 路由）。
func TestProxyAIRunListForwardsToOrchestrator(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	var gotToken, gotTenant string
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		gotTenant = r.Header.Get("X-Tenant-ID")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"runs":[]}`))
	}))
	defer orch.Close()

	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")
	setupProxyMySQL(mock) // RequestAuthorizationContext 需 identity + cluster 解析

	h := &Handler{}
	token := generateJWTWithSession(authzUserID, authzSessionID, "viewer", `{}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", authzTenantID)
	h.ProxyAI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", rec.Code, rec.Body.String())
	}
	if gotToken != "test-internal-token" {
		t.Fatalf("X-Internal-Token=%q, want test-internal-token", gotToken)
	}
	if gotTenant != authzTenantID {
		t.Fatalf("X-Tenant-ID=%q, want %q", gotTenant, authzTenantID)
	}
}

// TestProxyAIRunCreateForwardsPOST 验证 POST /api/v1/ai/runs 转发到 orchestrator（创建 Run 记录）。
func TestProxyAIRunCreateForwardsPOST(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	var gotToken, gotTenant, gotMethod string
	var gotBody string
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		gotTenant = r.Header.Get("X-Tenant-ID")
		gotMethod = r.Method
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"run":{"run_id":"x"}}`))
	}))
	defer orch.Close()

	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")
	setupProxyMySQL(mock)

	h := &Handler{}
	token := generateJWTWithSession(authzUserID, authzSessionID, "engineer", `{}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs", strings.NewReader(`{"intent":"diag"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", authzTenantID)
	req.Header.Set("Content-Type", "application/json")
	h.ProxyAI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", gotMethod)
	}
	if gotToken != "test-internal-token" {
		t.Fatalf("X-Internal-Token=%q", gotToken)
	}
	if gotTenant != authzTenantID {
		t.Fatalf("X-Tenant-ID=%q", gotTenant)
	}
	if !strings.Contains(gotBody, "diag") {
		t.Fatalf("body not forwarded: %q", gotBody)
	}
}

// TestProxyAIRunListDeniedWithoutJWT 验证 GET /api/v1/ai/runs 无 JWT → 拒绝（不转发）。
func TestProxyAIRunListDeniedWithoutJWT(t *testing.T) {
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.invalid")
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")
	h := &Handler{client: http.DefaultClient}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs", nil)
	h.ProxyAI(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 401/403", rec.Code)
	}
}

// TestProxyAIChatStillFailClosedForOtherAI 验证非 runs/chat 的 /api/v1/ai/* 仍 fail-closed。
func TestProxyAIChatStillFailClosedForOtherAI(t *testing.T) {
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.invalid")
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")
	h := &Handler{client: http.DefaultClient}
	for _, p := range []string{
		"/api/v1/ai/skills",
		"/api/v1/ai/agents",
		"/api/v1/ai/flows",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		h.ProxyAI(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d, want 403 (fail-closed)", p, rec.Code)
		}
	}
	_ = time.Now
}
