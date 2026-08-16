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

// QuotaAI=0 → 不限流：连续多次 /ai/chat 均 200 且都转发到 orchestrator。
func TestProxyAIQuotaUnlimited(t *testing.T) {
	resetQuotaUsage(t)
	setTestTenant(t, "default", 0) // 显式保证默认租户 QuotaAI=0
	h, forwarded := mockOrchHandler(t)

	for i := 0; i < 5; i++ {
		rec := proxyAIChat(t, h, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: quota=0 should be unlimited, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if *forwarded != 5 {
		t.Fatalf("forwarded=%d, want 5", *forwarded)
	}
}

// QuotaAI=3 → 第 4 次调用返回 429 且不转发；前 3 次正常转发。
func TestProxyAIQuotaExceeded(t *testing.T) {
	resetQuotaUsage(t)
	setTestTenant(t, "quota-tenant", 3)
	h, forwarded := mockOrchHandler(t)

	for i := 0; i < 5; i++ {
		rec := proxyAIChat(t, h, "quota-tenant")
		if i < 3 {
			if rec.Code != http.StatusOK {
				t.Fatalf("call %d: expected 200, got %d: %s", i+1, rec.Code, rec.Body.String())
			}
		} else {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("call %d: expected 429, got %d: %s", i+1, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "quota_ai_calls") {
				t.Fatalf("429 body should carry quota info: %s", rec.Body.String())
			}
		}
	}
	if *forwarded != 3 {
		t.Fatalf("forwarded=%d, want 3 (4th+ rejected before forwarding)", *forwarded)
	}
}

// 不同租户独立计数：tenant-a(2) 的第 3 次 429，tenant-b(1) 的第 2 次 429。
func TestProxyAIQuotaPerTenant(t *testing.T) {
	resetQuotaUsage(t)
	setTestTenant(t, "tenant-a", 2)
	setTestTenant(t, "tenant-b", 1)
	h, _ := mockOrchHandler(t)

	doCall := func(tenant string) int {
		return proxyAIChat(t, h, tenant).Code
	}
	// tenant-a：配额 2，第 3 次 429
	if code := doCall("tenant-a"); code != http.StatusOK {
		t.Fatalf("tenant-a call 1: got %d, want 200", code)
	}
	if code := doCall("tenant-a"); code != http.StatusOK {
		t.Fatalf("tenant-a call 2: got %d, want 200", code)
	}
	if code := doCall("tenant-a"); code != http.StatusTooManyRequests {
		t.Fatalf("tenant-a call 3: got %d, want 429", code)
	}
	// tenant-b：配额 1，第 2 次 429（计数独立于 tenant-a）
	if code := doCall("tenant-b"); code != http.StatusOK {
		t.Fatalf("tenant-b call 1: got %d, want 200 (independent counting)", code)
	}
	if code := doCall("tenant-b"); code != http.StatusTooManyRequests {
		t.Fatalf("tenant-b call 2: got %d, want 429", code)
	}
	// tenant-a 计数不受 tenant-b 影响（仍超限）
	if code := doCall("tenant-a"); code != http.StatusTooManyRequests {
		t.Fatalf("tenant-a call 4: got %d, want 429", code)
	}
}

// 租户不存在（非 default）→ 按不限处理（unlimited），不 429。
func TestProxyAIQuotaUnknownTenantUnlimited(t *testing.T) {
	resetQuotaUsage(t)
	h, forwarded := mockOrchHandler(t)

	for i := 0; i < 3; i++ {
		rec := proxyAIChat(t, h, "ghost-tenant")
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: unknown tenant should be unlimited, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if *forwarded != 3 {
		t.Fatalf("forwarded=%d, want 3", *forwarded)
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
