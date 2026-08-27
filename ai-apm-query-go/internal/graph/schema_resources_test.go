package graph

import "testing"

func TestSchemaResourceMatchesPropertyListsWithoutDependingOnHugeGraphOrder(t *testing.T) {
	want := map[string]interface{}{
		"properties":    []string{"edge_uid", "attrs_json", "generation"},
		"nullable_keys": []string{"edge_uid", "attrs_json", "generation"},
	}
	got := map[string]interface{}{
		"properties":    []interface{}{"generation", "edge_uid", "attrs_json"},
		"nullable_keys": []interface{}{"attrs_json", "generation", "edge_uid"},
	}
	if !schemaResourceMatches(want, got) {
		t.Fatal("schema property sets should match regardless of HugeGraph response order")
	}
}
