package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTripperFunc adapts a func to http.RoundTripper so we can inject a fake
// deepseek backend that returns a short error body (< 500 bytes).
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestModelsLLMShortErrorBodyDoesNotPanic 验证 P19 LLM 设置修复：当后端返回短错误响应
// （< 500 字节，如 deepseek 401 鉴权错误）时，ModelsLLM 不再因 `string(data)[:500]` 越界 panic。
func TestModelsLLMShortErrorBodyDoesNotPanic(t *testing.T) {
	h := &Handler{}
	// 短错误响应（153 字节，模拟 deepseek 401）
	shortBody := `{"error":{"message":"Authentication Fails","type":"authentication_error"}}`
	h.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 401,
				Status:     "401 Unauthorized",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewBufferString(shortBody)),
			}, nil
		}),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/llm/models",
		strings.NewReader(`{"base_url":"https://api.deepseek.com/v1","api_key":"sk-dummy"}`))
	h.ModelsLLM(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ModelsLLM short error body: status=%d, want 200 (no panic)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Authentication Fails") {
		t.Fatalf("ModelsLLM should surface the backend error, got: %s", body)
	}
	// 不应截断越界：raw 应完整包含短错误体
	if !strings.Contains(body, "authentication_error") {
		t.Fatalf("ModelsLLM raw should contain full short error body, got: %s", body)
	}
}

// TestModelsLLMLongErrorBodyTruncatedTo500 验证长响应被安全截断到 500，不越界。
func TestModelsLLMLongErrorBodyTruncatedTo500(t *testing.T) {
	h := &Handler{}
	longBody := strings.Repeat("x", 1000)
	h.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 500,
				Status:     "500",
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewBufferString(longBody)),
			}, nil
		}),
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/llm/models",
		strings.NewReader(`{"base_url":"https://api.deepseek.com/v1","api_key":"sk-dummy"}`))
	h.ModelsLLM(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	// 长响应被安全截断（不越界 panic），raw 长度被限制在 ~500
	if len(rec.Body.String()) > 600 {
		t.Fatalf("raw should be truncated, got response length %d", len(rec.Body.String()))
	}
}

func TestResolveLLMTestProviderPrefersRequestedProvider(t *testing.T) {
	if got, want := resolveLLMTestProvider("deepseek", "", "openai"), "deepseek"; got != want {
		t.Fatalf("provider = %q, want %q", got, want)
	}
	if got, want := resolveLLMTestProvider("", "", "openai"), "openai"; got != want {
		t.Fatalf("fallback provider = %q, want %q", got, want)
	}
}
