package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPanelCreateRequiresAdmin verifies the legacy panel write route fails
// closed until it has canonical MySQL authorization.
func TestPanelCreateRequiresAdmin(t *testing.T) {
	h := &Handler{client: &http.Client{}}

	// No token → 403 (no JWT role fallback)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/panels", strings.NewReader(`{"title":"rate","query":"sum(rate(x[5m]))"}`))
	h.DashboardRouter(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no token: code=%d, want 403", rec.Code)
	}

	// 非 admin token → 403
	userToken := generateJWT("zhangsan", "user", "")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/panels", strings.NewReader(`{"title":"rate","query":"q"}`))
	req2.Header.Set("Authorization", "Bearer "+userToken)
	h.DashboardRouter(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("non-admin: code=%d, want 403", rec2.Code)
	}
}

// TestPanelReadPublic 验证读面板无需鉴权。
func TestPanelReadPublic(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/panels", nil)
	h.DashboardRouter(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("GET panels should not require auth, got %d", rec.Code)
	}
}

// TestPanelDeleteRequiresAdmin 验证删除面板需 admin。
func TestPanelDeleteRequiresAdmin(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	userToken := generateJWT("zhangsan", "user", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dashboard/panels/panel-1", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	h.DashboardRouterByID(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete: code=%d, want 403", rec.Code)
	}
}
