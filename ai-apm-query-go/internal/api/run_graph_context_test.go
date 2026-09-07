package api

import (
	"encoding/json"
	"testing"
)

func TestEmptyRunGraphContextIsAnExplicitPartialProjection(t *testing.T) {
	value := emptyRunGraphContext("run-1")
	if value["run_id"] != "run-1" {
		t.Fatalf("run_id = %v, want run-1", value["run_id"])
	}
	if value["partial"] != true {
		t.Fatalf("partial = %v, want true", value["partial"])
	}
	if value["status"] != "not_generated" {
		t.Fatalf("status = %v, want not_generated", value["status"])
	}
	warnings, ok := value["warning_codes"].([]string)
	if !ok || len(warnings) != 1 || warnings[0] != "GRAPH_CONTEXT_NOT_GENERATED" {
		t.Fatalf("warning_codes = %#v, want explicit missing projection warning", value["warning_codes"])
	}
	if _, err := json.Marshal(value); err != nil {
		t.Fatalf("empty graph context is not JSON serializable: %v", err)
	}
}
