package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSLOCreateFailsClosedWithoutCanonicalAuthorization verifies the legacy
// SLO write route cannot use JWT role claims as authority.
func TestSLOCreateRequiresAdmin(t *testing.T) {
	h := &Handler{client: &http.Client{}}

	// No token and a forged role claim both fail closed until this route gains a
	// canonical AuthorizationDAO action/scope mapping.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/slo", strings.NewReader(`{"name":"payments","service":"payments"}`))
	h.SLORouter(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no token: code=%d, want 403", rec.Code)
	}

	// 非 admin（user 角色）→ 403
	userToken := generateJWT("alice", "user", "")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/slo", strings.NewReader(`{"name":"payments","service":"payments"}`))
	req2.Header.Set("Authorization", "Bearer "+userToken)
	h.SLORouter(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("non-admin: code=%d, want 403", rec2.Code)
	}
}

// TestSLOListNoAuth 验证读 SLO 列表无需鉴权（返回数据或空）。
func TestSLOListNoAuth(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	// 无 DB 时 List 返回 mysql unavailable → 500；但鉴权应放行到 handler（非 401/403）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/slo", nil)
	h.SLORouter(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("GET /slo should not require auth, got %d", rec.Code)
	}
}

// TestSLODeleteRequiresAdmin 验证 DELETE 需 admin。
func TestSLODeleteRequiresAdmin(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	userToken := generateJWT("alice", "user", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/slo/abc", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	h.SLORouterByID(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete: code=%d, want 403", rec.Code)
	}
}
