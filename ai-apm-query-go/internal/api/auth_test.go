package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequireRoleForWrite verifies a JWT role claim cannot authorize legacy writes.
func TestRequireRoleForWrite(t *testing.T) {
	h := &Handler{}
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true }

	// Legacy routes do not have a canonical MySQL authorization action/scope yet,
	// so even reads fail closed instead of treating a missing or JWT-derived role
	// as authority.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("GET legacy route: got status=%d called=%v, want denied without handler call", rec.Code, called)
	}
	called = false

	// POST（写）无 token → 403
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without token: got %d, want 403", rec.Code)
	}
	if called {
		t.Fatal("handler should not be called for unauthorized POST")
	}

	// POST（写）普通用户 token → 403
	userTok := generateJWT("user1", "viewer", "")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST as viewer: got %d, want 403", rec.Code)
	}

	// A forged/admin JWT claim remains insufficient: current MySQL authorization
	// is required by the protected request boundary, so this legacy wrapper
	// fails closed when invoked directly.
	adminTok := generateJWT("admin1", "admin", "")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("POST with JWT admin claim: got status=%d called=%v, want denied without handler call", rec.Code, called)
	}
}

// TestValidateJWTStandard 验证真实 HS256 JWT 签发与校验。
func TestValidateJWTStandard(t *testing.T) {
	token := generateJWT("admin", "admin", `{"clusters":["all"]}`)
	u, role, scope, ok := validateJWT(token)
	if !ok {
		t.Fatal("valid token rejected")
	}
	if u != "admin" || role != "" || scope != "" {
		t.Fatalf("got identity=%q role=%q scope=%q, want identity-only claims", u, role, scope)
	}
}

// TestTamperedTokenRejected 验证被篡改的 token 会被拒绝。
func TestTamperedTokenRejected(t *testing.T) {
	token := generateJWT("admin", "admin", "")
	bad := token[:len(token)-3] + "xxx"
	if _, _, _, ok := validateJWT(bad); ok {
		t.Fatal("tampered token accepted")
	}
}

// TestExpiredTokenRejected 验证过期 token 被拒绝。
func TestExpiredTokenRejected(t *testing.T) {
	// 签发一个 exp 为过去的 token（直接构造）
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsInJvbGUiOiJhZG1pbiIsImV4cCI6MX0.invalid"
	if _, _, _, ok := validateJWT(token); ok {
		t.Fatal("expired token accepted")
	}
}

// TestScopeParsing 验证 scope 解析与包含判断。
func TestScopeParsing(t *testing.T) {
	sc := parseScope(`{"services":["a","b"],"clusters":[],"devices":[]}`)
	if !sc.ContainsService("a") || sc.ContainsService("c") {
		t.Fatal("service scope parse wrong")
	}
	if !sc.ContainsCluster("any") { // clusters 未限定 => 全通过
		t.Fatal("unscoped dimension should pass")
	}
	if sc.IsFull() {
		t.Fatal("non-empty scope should not be full")
	}
	if !parseScope("").IsFull() {
		t.Fatal("empty scope should be full")
	}
}
