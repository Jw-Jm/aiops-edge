package contract

import (
	"encoding/json"
	"testing"
)

func TestCanonicalActionHashIncludesTargetAndParams(t *testing.T) {
	action := CanonicalActionPayloadV2{
		Version:         1,
		ActionType:      "kubernetes",
		ResourceType:    "deployment",
		Namespace:       "prod",
		TargetName:      "orders",
		TargetUID:       "uid-1",
		ResourceVersion: "42",
		Operation:       "scale",
		Params:          json.RawMessage(`{"replicas":2}`),
		PolicyVersion:   "action-policy-v1",
	}
	first, err := CanonicalActionHash(action)
	if err != nil {
		t.Fatal(err)
	}
	action.ResourceVersion = "43"
	second, err := CanonicalActionHash(action)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("resourceVersion must change the action hash")
	}
}

func TestCanonicalActionHashNormalizesObjectKeyOrder(t *testing.T) {
	base := CanonicalActionPayloadV2{
		Version: 1, ActionType: "kubernetes", ResourceType: "deployment",
		Namespace: "prod", TargetName: "orders", TargetUID: "uid-1",
		ResourceVersion: "42", Operation: "scale", PolicyVersion: "action-policy-v1",
	}
	base.Params = json.RawMessage(`{"replicas":2,"reason":"slo"}`)
	first, err := CanonicalActionHash(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Params = json.RawMessage(`{"reason":"slo","replicas":2}`)
	second, err := CanonicalActionHash(base)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("object key order must not change the canonical hash")
	}
}
