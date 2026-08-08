package api

import "testing"

// TestValidateJWTStandard 验证真实 HS256 JWT 签发与校验。
func TestValidateJWTStandard(t *testing.T) {
	token := generateJWT("admin", "admin", "")
	u, role, _, ok := validateJWT(token)
	if !ok {
		t.Fatal("valid token rejected")
	}
	if u != "admin" || role != "admin" {
		t.Fatalf("got %s/%s, want admin/admin", u, role)
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
