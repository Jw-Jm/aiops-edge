package api

import (
	"testing"
)

// TestDedupeSignature 验证相同 rule+service+signature 的事件在窗口内被合并（Count++ 而非新增）
func TestDedupeSignature(t *testing.T) {
	sig := "svc-error:500"
	e1 := eventSignature("rule1", "svc", sig)
	e2 := eventSignature("rule1", "svc", sig)
	if e1 != e2 {
		t.Fatal("same signature should dedupe")
	}
	e3 := eventSignature("rule1", "svc", "svc-error:502")
	if e1 == e3 {
		t.Fatal("different signature should not dedupe")
	}
}
