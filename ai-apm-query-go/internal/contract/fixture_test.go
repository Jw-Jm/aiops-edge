package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type contractFixture struct {
	RunInvocationContext RunInvocationContext  `json:"run_invocation_context"`
	RunControlContext    RunControlContext     `json:"run_control_context"`
	TrustedRequestContext TrustedRequestContext `json:"trusted_request_context"`
	Resources            []ResourceRef         `json:"resources"`
	ToolResultSuccess    ToolResult            `json:"tool_result_success"`
	ToolResultEmptyNoData ToolResult           `json:"tool_result_empty_no_data"`
	Evidence             json.RawMessage       `json:"evidence"`
	Hypothesis           json.RawMessage       `json:"hypothesis"`
	OpsAction            json.RawMessage       `json:"ops_action"`
	Verification         json.RawMessage       `json:"verification"`
	// P3.10c-final: shared fixture carries a structured CLUSTER_IDENTITY_MISMATCH
	// error to prove the cross-language error-code surface agrees.
	ClusterIdentityMismatchError StructuredError `json:"cluster_identity_mismatch_error"`
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

	if err := fixture.RunInvocationContext.Validate(); err != nil {
		t.Fatalf("validate run invocation context: %v", err)
	}
	if err := fixture.RunControlContext.Validate(); err != nil {
		t.Fatalf("validate run control context: %v", err)
	}
	if err := fixture.TrustedRequestContext.Validate(); err != nil {
		t.Fatalf("validate trusted request context: %v", err)
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

	if err := fixture.ToolResultSuccess.Validate(); err != nil {
		t.Fatalf("validate tool result success: %v", err)
	}
	// V9.2: success=true with empty result → status=no_data
	if err := fixture.ToolResultEmptyNoData.Validate(); err != nil {
		t.Fatalf("validate tool result empty no_data: %v", err)
	}
	if !fixture.ToolResultEmptyNoData.Success || fixture.ToolResultEmptyNoData.Status != "no_data" {
		t.Fatal("empty successful tool result must be success=true, status=no_data")
	}

	if _, err := json.Marshal(fixture.TrustedRequestContext); err != nil {
		t.Fatalf("marshal trusted request context: %v", err)
	}

	// P3.10c-final: the shared fixture's cluster identity mismatch must decode and
	// map to 409 Conflict (a binding conflict, not a backend outage).
	if fixture.ClusterIdentityMismatchError.ErrorCode != ErrorCodeClusterIdentityMismatch {
		t.Fatalf("fixture mismatch error_code = %q, want %q", fixture.ClusterIdentityMismatchError.ErrorCode, ErrorCodeClusterIdentityMismatch)
	}
	if got := HTTPStatusCode(ErrorCodeClusterIdentityMismatch); got != 409 {
		t.Fatalf("HTTPStatusCode(CLUSTER_IDENTITY_MISMATCH) = %d, want 409", got)
	}
}
