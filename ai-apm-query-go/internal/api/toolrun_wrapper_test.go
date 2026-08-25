package api

import "testing"

func TestInvestigationToolRequestRequiresLeaseBoundContext(t *testing.T) {
	req := &internalQueryRequest{WorkloadKind: "investigation", RunID: ""}
	if err := validateToolRunRequest(req); err == nil {
		t.Fatal("investigation query without ToolRun/Run lease context must be rejected")
	}
}

func TestToolArgsHashDistinguishesFullNumericArguments(t *testing.T) {
	a := &internalQueryRequest{Service: "orders", Minutes: 1}
	b := &internalQueryRequest{Service: "orders", Minutes: 257}
	if toolArgsHash(a) == toolArgsHash(b) {
		t.Fatal("args hash must distinguish numeric parameters beyond one byte")
	}
}

func TestWorkloadKindMatchRejectsUnsignedInvestigation(t *testing.T) {
	if err := checkWorkloadKindMatch("platform", "investigation"); err == nil {
		t.Fatal("body-only investigation workload must be rejected")
	}
	if err := checkWorkloadKindMatch("", "investigation"); err == nil {
		t.Fatal("missing signed workload must not authorize investigation")
	}
}

func TestWorkloadKindMatchRejectsInvestigationDowngrade(t *testing.T) {
	if err := checkWorkloadKindMatch("investigation", ""); err == nil {
		t.Fatal("omitted investigation workload must be rejected")
	}
	if err := checkWorkloadKindMatch("investigation", "chat"); err == nil {
		t.Fatal("chat downgrade must be rejected")
	}
}
