package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestRuntimeLeaseNewToken 验证 lease token 生成：明文与 hash 的关系。
func TestRuntimeLeaseNewToken(t *testing.T) {
	raw, h := NewLeaseToken()
	if raw == "" || h == "" {
		t.Fatalf("empty token/hash")
	}
	sum := sha256.Sum256([]byte(raw))
	if h != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash mismatch")
	}
	raw2, _ := NewLeaseToken()
	if raw == raw2 {
		t.Fatalf("tokens should be unique")
	}
}
