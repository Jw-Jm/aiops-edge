package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type contractFixture struct {
	RequestContext RequestContext  `json:"request_context"`
	Resources      []ResourceRef   `json:"resources"`
	ToolResult     ToolResult      `json:"tool_result"`
	Evidence       json.RawMessage `json:"evidence"`
	Hypothesis     json.RawMessage `json:"hypothesis"`
	OpsAction      json.RawMessage `json:"ops_action"`
	Verification   json.RawMessage `json:"verification"`
}

func TestSharedContractFixture(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "contracts", "contract-fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}

	var fixture contractFixture
	if err := DecodeStrict(data, &fixture); err != nil {
		t.Fatalf("decode shared fixture: %v", err)
	}
	if err := fixture.RequestContext.Validate(); err != nil {
		t.Fatalf("validate request context: %v", err)
	}
	for index := range fixture.Resources {
		if err := fixture.Resources[index].Validate(); err != nil {
			t.Fatalf("validate resource %d: %v", index, err)
		}
	}
	if fixture.Resources[0].Name != fixture.Resources[1].Name {
		t.Fatal("fixture must cover same-name resources")
	}
	if fixture.Resources[0].ClusterID == fixture.Resources[1].ClusterID {
		t.Fatal("same-name resources must have different canonical clusters")
	}
	if err := fixture.ToolResult.Validate(); err != nil {
		t.Fatalf("validate tool result: %v", err)
	}

	if _, err := json.Marshal(fixture.RequestContext); err != nil {
		t.Fatalf("marshal request context: %v", err)
	}
}
