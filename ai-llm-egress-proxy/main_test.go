package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderTargetPathStripsProviderSegment(t *testing.T) {
	target, err := providerTarget("https://api.openai.com", "chat/completions", "model=gpt-4o")
	if err != nil {
		t.Fatalf("providerTarget: %v", err)
	}
	if target.Path != "/v1/chat/completions" {
		t.Fatalf("target path = %q, want /v1/chat/completions", target.Path)
	}
	if target.RawQuery != "model=gpt-4o" {
		t.Fatalf("target query = %q, want model=gpt-4o", target.RawQuery)
	}
}

func TestConfiguredAllowlistControlsProvider(t *testing.T) {
	cfg := &proxyConfig{
		providerKeys: map[string]string{"deepseek": "sk-test"},
		baseURLs:     map[string]string{"deepseek": "https://api.deepseek.com"},
		allowlist:    map[string]struct{}{"api.deepseek.com": {}},
	}
	if !cfg.providerAllowlisted("deepseek") {
		t.Fatal("configured host allowlist should permit deepseek")
	}
	cfg.allowlist = map[string]struct{}{"api.openai.com": {}}
	if cfg.providerAllowlisted("deepseek") {
		t.Fatal("configured allowlist should reject a provider outside the configured hosts")
	}
}

func TestAllowlisted(t *testing.T) {
	if !allowlisted("deepseek") || !allowlisted("openai") {
		t.Fatal("deepseek/openai should be allowlisted")
	}
	if allowlisted("evil.example.com") {
		t.Fatal("non-allowlisted provider must be rejected")
	}
}

func TestHandleProxyRequiresToken(t *testing.T) {
	cfg := &proxyConfig{
		providerKeys: map[string]string{"deepseek": "sk-test"},
		baseURLs:     map[string]string{"deepseek": "https://api.deepseek.com"},
		proxyToken:   "secret-token",
		client:       &http.Client{},
	}
	// 无 token → 403
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/deepseek/chat/completions", nil)
	rec := httptest.NewRecorder()
	cfg.handleProxy(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without token, got %d", rec.Code)
	}
}

func TestHandleProxyUnknownProvider(t *testing.T) {
	cfg := &proxyConfig{
		providerKeys: map[string]string{"deepseek": "sk-test"},
		baseURLs:     map[string]string{"deepseek": "https://api.deepseek.com"},
		client:       &http.Client{},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/unknown/chat/completions", nil)
	rec := httptest.NewRecorder()
	cfg.handleProxy(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown provider, got %d", rec.Code)
	}
}
