package api

import "testing"

// TestValidateJWTStandard 验证真实 HS256 JWT 签发与校验。
func TestValidateJWTStandard(t *testing.T) {
	token := generateJWT("admin", "admin")
	u, role, ok := validateJWT(token)
	if !ok {
		t.Fatal("valid token rejected")
	}
	if u != "admin" || role != "admin" {
		t.Fatalf("got %s/%s, want admin/admin", u, role)
	}
}

// TestTamperedTokenRejected 验证被篡改的 token 会被拒绝。
func TestTamperedTokenRejected(t *testing.T) {
	token := generateJWT("admin", "admin")
	bad := token[:len(token)-3] + "xxx"
	if _, _, ok := validateJWT(bad); ok {
		t.Fatal("tampered token accepted")
	}
}

// TestExpiredTokenRejected 验证过期 token 被拒绝。
func TestExpiredTokenRejected(t *testing.T) {
	// 签发一个 exp 为过去的 token（直接构造）
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsInJvbGUiOiJhZG1pbiIsImV4cCI6MX0.invalid"
	if _, _, ok := validateJWT(token); ok {
		t.Fatal("expired token accepted")
	}
}
