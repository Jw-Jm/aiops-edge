package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 测试辅助：写入租户配额，测试结束后删除该租户恢复原状。
func setTestTenant(t *testing.T, id string, quotaAI int) {
	t.Helper()
	tenantsMu.Lock()
	tenants[id] = &Tenant{ID: id, Name: id, QuotaAI: quotaAI, Enabled: true}
	tenantsMu.Unlock()
	t.Cleanup(func() {
		tenantsMu.Lock()
		delete(tenants, id)
		tenantsMu.Unlock()
	})
}

// 测试辅助：清空当日配额计数（避免跨测试互相污染）。
func resetQuotaUsage(t *testing.T) {
	t.Helper()
	quotaUsageMu.Lock()
	quotaUsage = map[string]int{}
	quotaUsageMu.Unlock()
}

// mockOrchHandler 构造 mock ai-orchestrator 与 Handler，并统计转发次数。
func mockOrchHandler(t *testing.T) (*Handler, *int) {
	t.Helper()
	forwarded := 0
	orchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(orchSrv.Close)
	t.Setenv("AI_ORCHESTRATOR_URL", orchSrv.URL)
	return &Handler{client: orchSrv.Client()}, &forwarded
}

// proxyAIChat 发起一次 /ai/chat 代理请求（带可选 X-Tenant-ID）。
func proxyAIChat(t *testing.T, h *Handler, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", strings.NewReader(`{"msg":"hi"}`))
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	h.ProxyAI(rec, req)
	return rec
}

// Legacy ProxyAI traffic is denied before quota evaluation until a newly signed
// canonical context caller exists.
func TestProxyAIQuotaUnlimited(t *testing.T) {
	resetQuotaUsage(t)
	setTestTenant(t, "default", 0) // 显式保证默认租户 QuotaAI=0
	h, forwarded := mockOrchHandler(t)

	for i := 0; i < 5; i++ {
		rec := proxyAIChat(t, h, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("call %d: expected fail-closed 403, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if *forwarded != 0 {
		t.Fatalf("forwarded=%d, want 0", *forwarded)
	}
}

// A configured tenant quota cannot turn a token-only proxy request into an
// internal service call.
func TestProxyAIQuotaExceeded(t *testing.T) {
	resetQuotaUsage(t)
	setTestTenant(t, "quota-tenant", 3)
	h, forwarded := mockOrchHandler(t)

	for i := 0; i < 5; i++ {
		rec := proxyAIChat(t, h, "quota-tenant")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("call %d: expected fail-closed 403, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if *forwarded != 0 {
		t.Fatalf("forwarded=%d, want 0", *forwarded)
	}
}

// A requested tenant header cannot bypass the proxy's signed-context boundary.
func TestProxyAIQuotaPerTenant(t *testing.T) {
	resetQuotaUsage(t)
	setTestTenant(t, "tenant-a", 2)
	setTestTenant(t, "tenant-b", 1)
	h, forwarded := mockOrchHandler(t)

	doCall := func(tenant string) int {
		return proxyAIChat(t, h, tenant).Code
	}
	for _, tenant := range []string{"tenant-a", "tenant-a", "tenant-b", "tenant-b", "tenant-a"} {
		if code := doCall(tenant); code != http.StatusForbidden {
			t.Fatalf("%s: got %d, want fail-closed 403", tenant, code)
		}
	}
	if *forwarded != 0 {
		t.Fatalf("forwarded=%d, want 0", *forwarded)
	}
}

// An unknown tenant hint cannot create a default or implicit proxy scope.
func TestProxyAIQuotaUnknownTenantUnlimited(t *testing.T) {
	resetQuotaUsage(t)
	h, forwarded := mockOrchHandler(t)

	for i := 0; i < 3; i++ {
		rec := proxyAIChat(t, h, "ghost-tenant")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("call %d: expected fail-closed 403, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if *forwarded != 0 {
		t.Fatalf("forwarded=%d, want 0", *forwarded)
	}
}

// isLLMProxyPath：/ai/chat、/ai/nl2sql、/ai/final_report 计配额，工具类路径不计。
func TestProxyAIIsLLMPath(t *testing.T) {
	llmPaths := []string{
		"/api/v1/ai/chat",
		"/api/v1/ai/chat/123",
		"/api/v1/ai/nl2sql",
		"/api/v1/ai/nl2sql/translate",
		"/api/v1/ai/final_report",
	}
	for _, p := range llmPaths {
		if !isLLMProxyPath(p) {
			t.Fatalf("isLLMProxyPath(%q) should be true", p)
		}
	}
	nonLLMPaths := []string{
		"/api/v1/ai/sessions",
		"/api/v1/ai/sessions/123",
		"/api/v1/ai/shell/check",
		"/api/v1/ai/skills",
		"/api/v1/ai/agents",
		"/api/v1/mcp/tools",
		"/api/v1/ai/workflows",
	}
	for _, p := range nonLLMPaths {
		if isLLMProxyPath(p) {
			t.Fatalf("isLLMProxyPath(%q) should be false (tool path)", p)
		}
	}
}
