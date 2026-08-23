package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.5 Authorization A：internal service boundary 接线修复测试。
//
// 背景：/internal/v1/query/*（P6.2e）被公共 AuthMiddleware 误拦截——
//   isCanonicalProtectedRoute 只放行 /api/v1/resources/resolve，其它路径在
//   AuthMiddleware 返回 403 permission_denied，导致 InternalQuery* handler
//   永远不可达（其自身的 authorizeInternalQuery / TrustedRequestContext V2
//   校验永远不会执行）。
//
// 修复契约（最小、不改变公共 API 安全边界）：
//   - AuthMiddleware 对 /internal/v1/* 前缀直接放行（internal handler 自鉴权）
//   - /api/v1/* 公共 API 的 JWT + canonical tenant 校验保持不变
//   - internal route 缺 TrustedRequestContext 时失败点必须是 authorizeInternalQuery
//     （401 SERVICE_AUTH_FAILED），而不是 AuthMiddleware JWT 层（403 permission_denied）
// ─────────────────────────────────────────────────────────────────────────────

// newInternalMux 构造 AuthMiddleware 包裹的 mux，模拟 main.go:350-361 注册。
func newInternalMux(t *testing.T) (*internalQueryTestCtx, http.Handler) {
	t.Helper()
	// ServiceRED 走 ClickHouseRepo.Query（POST body），mock 输出一行 TabSeparated 采样点。
	redRows := "2026-08-20 10:00:00\t10\t1\t125.5\n"
	c := newInternalQueryTestHandler(t, map[string]string{"FROM observability.trace_spans": redRows})
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/query/metrics", c.h.InternalQueryMetrics)
	mux.HandleFunc("/internal/v1/query/logs", c.h.InternalQueryLogs)
	mux.HandleFunc("/api/v1/logs/query", c.h.QueryLogs) // public 占位，验证不受影响
	return c, AuthMiddleware(mux)
}

// 正常 internal route：带 X-Internal-Token + TrustedRequestContext + signature
// → 必须到达 InternalQuery handler（由 authorizeInternalQuery 放行）。
func TestAuthMiddlewareInternalRouteReachesHandler(t *testing.T) {
	c, mux := newInternalMux(t)
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/metrics", `{"services":["checkout"]}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "observability.metrics.read"
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("internal route with valid TrustedRequestContext must reach handler: got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "points") {
		t.Fatalf("expected handler response with points, got %s", rec.Body.String())
	}
}

// 缺少 TrustedRequestContext：失败点必须是 authorizeInternalQuery（401 SERVICE_AUTH_FAILED），
// 而不是 AuthMiddleware JWT 层（403 permission_denied）。
func TestAuthMiddlewareInternalRouteMissingContextFailsAtInternalBoundary(t *testing.T) {
	_, mux := newInternalMux(t)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/query/metrics", strings.NewReader(`{"services":["checkout"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "test-service-token") // service token but NO trusted context
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing TrustedRequestContext must fail at authorizeInternalQuery (401), got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "SERVICE_AUTH_FAILED") {
		t.Fatalf("expected SERVICE_AUTH_FAILED from internal boundary, got %s", rec.Body.String())
	}
}

// 公共 API 不受影响：/api/v1/* 无 JWT 时仍被 AuthMiddleware 拦截（不是放行）。
func TestAuthMiddlewarePublicAPIRemainsProtected(t *testing.T) {
	_, mux := newInternalMux(t)
	for _, path := range []string{"/api/v1/logs/query", "/api/v1/metrics/query"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("public API %s must remain protected without JWT, got 200", path)
		}
	}
}

// 内部边界：带正确 service token + 签名 context 但 capability 不符 → 由
// authorizeInternalQuery 拒绝（403 TENANT_ACCESS_DENIED），不是 AuthMiddleware。
func TestAuthMiddlewareInternalRouteWrongCapabilityRejectedAtInternalBoundary(t *testing.T) {
	c, mux := newInternalMux(t)
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/metrics", `{"services":["checkout"]}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "observability.logs.read" // 与 metrics route 要求不符
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong capability must fail at authorizeInternalQuery (403), got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unauthorized capability") {
		t.Fatalf("expected unauthorized capability message, got %s", rec.Body.String())
	}
}
