package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
