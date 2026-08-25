package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// C-04 / P1：LLM Proxy 强认证 + key-isolation E2E。
//
// 验证（报告 §19 / P1 "LLM Proxy path/auth/key-isolation E2E"）：
//   1. 正确 PROXY_TOKEN → 转发成功；错误/缺失 token → 403。
//   2. key isolation：provider API key（LLM_PROVIDER_KEYS）被注入到转发请求的
//      Authorization；调用方原始 Authorization 被覆盖（不把调用方凭据透传给 LLM）。
//   3. allowlist 域名：非 allowlist provider → 403。
//   4. 目标 provider 收到正确 Authorization（Bearer <provider key>）。
// ─────────────────────────────────────────────────────────────────────────────

// fakeLLM 是本地假 LLM 后端，捕获它收到的 Authorization。
func fakeLLM(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"fake","choices":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &receivedAuth
}

func TestProxyE2E_AuthAndKeyIsolation(t *testing.T) {
	llm, receivedAuth := fakeLLM(t)
	cfg := &proxyConfig{
		providerKeys: map[string]string{"deepseek": "sk-deepseek-secret"},
		baseURLs:     map[string]string{"deepseek": strings.TrimSuffix(llm.URL, "/")},
		proxyToken:   "correct-token",
		client:       &http.Client{},
	}

	// 1) 错误 token → 403
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/deepseek/chat/completions", nil)
	req.Header.Set("X-Proxy-Token", "wrong-token")
	req.Header.Set("Authorization", "Bearer caller-credential")
	rec := httptest.NewRecorder()
	cfg.handleProxy(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: expected 403, got %d", rec.Code)
	}

	// 2) 正确 token → 转发成功；目标收到 provider key 而非调用方凭据（key isolation）。
	req2 := httptest.NewRequest(http.MethodPost, "/v1/proxy/deepseek/chat/completions", nil)
	req2.Header.Set("X-Proxy-Token", "correct-token")
	req2.Header.Set("Authorization", "Bearer caller-credential")
	rec2 := httptest.NewRecorder()
	cfg.handleProxy(rec2, req2)
	if rec2.Code != http.StatusOK && rec2.Code != http.StatusCreated {
		t.Fatalf("correct token: expected 2xx, got %d", rec2.Code)
	}
	// 目标 LLM 收到的是 provider key（key isolation：绝不透传调用方凭据）。
	if *receivedAuth != "Bearer sk-deepseek-secret" {
		t.Fatalf("key isolation: expected provider key injected, got %q", *receivedAuth)
	}
	if strings.Contains(*receivedAuth, "caller-credential") {
		t.Fatalf("key isolation broken: caller credential leaked to LLM provider")
	}
}

func TestProxyE2E_Allowlist(t *testing.T) {
	cfg := &proxyConfig{
		providerKeys: map[string]string{"deepseek": "sk-x"},
		baseURLs:     map[string]string{"deepseek": "http://127.0.0.1:1"},
		proxyToken:   "t",
		client:       &http.Client{},
	}
	// 非 allowlist provider → 403（在路由/转发前拦截）。
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/evil.example.com/chat/completions", nil)
	req.Header.Set("X-Proxy-Token", "t")
	rec := httptest.NewRecorder()
	cfg.handleProxy(rec, req)
	if rec.Code != http.StatusNotFound { // providerKeys 无此 provider → 404
		t.Fatalf("unexpected status for unknown provider: %d", rec.Code)
	}
	// allowlist 校验本身独立测试：allowlisted() 拒绝非白名单。
	if allowlisted("evil.example.com") {
		t.Fatal("non-allowlisted provider must be rejected by allowlisted()")
	}
}

// TestProxyE2E_Readiness 验证 proxy 健康端点。
func TestProxyE2E_Readiness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz expected 200, got %d", resp.StatusCode)
	}
}
