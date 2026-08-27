package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGrafanaSearchPassthrough: GET /api/v1/grafana/search?query= 应透传上游 /api/search 的
// 状态码与 JSON 响应体，并把 query 参数原样带到上游。
func TestGrafanaSearchPassthrough(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"uid":"abc","title":"DeepFlow 总览","tags":["deepflow"]}]`))
	}))
	defer upstream.Close()

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/search?query=deepflow", nil)
	gh.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPath != "/api/search" {
		t.Fatalf("upstream path = %q, want /api/search", gotPath)
	}
	if gotQuery != "query=deepflow" {
		t.Fatalf("upstream query = %q, want query=deepflow", gotQuery)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization should be empty without token, got %q", gotAuth)
	}
	wantBody := `[{"uid":"abc","title":"DeepFlow 总览","tags":["deepflow"]}]`
	if strings.TrimSpace(rec.Body.String()) != wantBody {
		t.Fatalf("body = %q, want %q", rec.Body.String(), wantBody)
	}
}

// TestGrafanaDashboardPassthrough: GET /api/v1/grafana/dashboards/{uid} 应转发到上游
// /api/dashboards/uid/{uid} 并透传 dashboard JSON。
func TestGrafanaDashboardPassthrough(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dashboard":{"uid":"abc","title":"某看板"},"meta":{"type":"db"}}`))
	}))
	defer upstream.Close()

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/dashboards/abc", nil)
	gh.ProxyDashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPath != "/api/dashboards/uid/abc" {
		t.Fatalf("upstream path = %q, want /api/dashboards/uid/abc", gotPath)
	}
	wantBody := `{"dashboard":{"uid":"abc","title":"某看板"},"meta":{"type":"db"}}`
	if strings.TrimSpace(rec.Body.String()) != wantBody {
		t.Fatalf("body = %q, want %q", rec.Body.String(), wantBody)
	}
}

// TestGrafanaDashboardMissingUID: uid 缺失应返回 400。
func TestGrafanaDashboardMissingUID(t *testing.T) {
	gh := NewGrafanaHandler(GrafanaConfig{RootURL: "http://unused", TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/dashboards/", nil)
	gh.ProxyDashboard(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestGrafanaUpstream404Passthrough: 上游 404 应透传 404 状态码与错误体。
func TestGrafanaUpstream404Passthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Dashboard not found"}`))
	}))
	defer upstream.Close()

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/dashboards/ghost", nil)
	gh.ProxyDashboard(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Dashboard not found") {
		t.Fatalf("body = %q, want to contain upstream error message", rec.Body.String())
	}
}

func TestGrafanaUpstreamAuthFailureDoesNotLookLikePlatform401(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Grafana login required"}`))
	}))
	defer upstream.Close()

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: true})
	rec := httptest.NewRecorder()
	gh.Search(rec, httptest.NewRequest(http.MethodGet, "/api/v1/grafana/search?query=MySQL", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for upstream Grafana auth failure", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "grafana authentication failed") {
		t.Fatalf("body = %q, want a non-sensitive Grafana auth error", rec.Body.String())
	}
}

// TestGrafanaAuthorizationHeader: 配置 APIToken 时上游请求应带 Bearer Authorization。
func TestGrafanaAuthorizationHeader(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, APIToken: "tok-secret-123", TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/search?query=mem", nil)
	gh.Search(rec, req)

	if gotAuth != "Bearer tok-secret-123" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer tok-secret-123")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestGrafanaHealthPassthrough: GET /api/v1/grafana/health 应转发上游 /api/health。
func TestGrafanaHealthPassthrough(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"database":"ok","version":"11.0.0"}`))
	}))
	defer upstream.Close()

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/health", nil)
	gh.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPath != "/api/health" {
		t.Fatalf("upstream path = %q, want /api/health", gotPath)
	}
	if !strings.Contains(rec.Body.String(), `"database":"ok"`) {
		t.Fatalf("body = %q, want upstream health JSON", rec.Body.String())
	}
}

// TestGrafanaUpstreamUnreachable502: 上游连接失败应映射 502。
func TestGrafanaUpstreamUnreachable502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := upstream.URL
	upstream.Close() // 立即关闭 → 后续请求连接拒绝

	gh := NewGrafanaHandler(GrafanaConfig{RootURL: deadURL, TLSInsecure: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/grafana/search?query=x", nil)
	gh.Search(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// TestGrafanaTLSInsecureBehavior: TLSInsecure=true 可连通自签 TLS 上游（InsecureSkipVerify），
// TLSInsecure=false 时应因证书校验失败返回 502。
func TestGrafanaTLSInsecureBehavior(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"database":"ok"}`))
	}))
	defer upstream.Close()

	// insecure=true：自签证书应放行
	ghOK := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: true})
	recOK := httptest.NewRecorder()
	ghOK.Health(recOK, httptest.NewRequest(http.MethodGet, "/api/v1/grafana/health", nil))
	if recOK.Code != http.StatusOK {
		t.Fatalf("TLSInsecure=true: status = %d, want 200 (should skip verify)", recOK.Code)
	}

	// insecure=false：自签证书应校验失败 → 连接失败 → 502
	ghBad := NewGrafanaHandler(GrafanaConfig{RootURL: upstream.URL, TLSInsecure: false})
	recBad := httptest.NewRecorder()
	ghBad.Health(recBad, httptest.NewRequest(http.MethodGet, "/api/v1/grafana/health", nil))
	if recBad.Code != http.StatusBadGateway {
		t.Fatalf("TLSInsecure=false: status = %d, want 502 (cert verify should fail)", recBad.Code)
	}
}
