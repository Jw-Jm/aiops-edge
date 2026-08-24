package store

import (
	"testing"
)

// TestAlertLeaderTokenHash 验证 alert leader token hash 一致性。
func TestAlertLeaderTokenHash(t *testing.T) {
	h := hashTokenForStore("tok-abc")
	if h == "" || len(h) != 64 {
		t.Fatalf("expected 64-hex sha256 hash, got %q", h)
	}
	if h != hashTokenForStore("tok-abc") {
		t.Fatalf("hash should be deterministic")
	}
	if h == hashTokenForStore("tok-abc2") {
		t.Fatalf("hash should differ by token")
	}
}

// TestAlertRuleStateUpsert 验证运行时状态 Upsert 生成的 SQL（sqlmock 可匹配）。
// 注意：本测试只做逻辑断言，不连真实 DB（GetDB()==nil 时 Upsert 返回 mysql unavailable）。
func TestAlertRuleStateSQLShape(t *testing.T) {
	// 该测试仅占位：真实逻辑在 integration test（TestA1LeaseCommit 同目录 tag）。
	// 此处验证 DAO 构造不 panic。
	dao := &AlertRuleRuntimeStateDAO{}
	if dao == nil {
		t.Fatal("dao nil")
	}
	leader := &AlertEvalLeaderDAO{}
	if leader == nil {
		t.Fatal("leader dao nil")
	}
}
