package api

import (
	"encoding/json"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

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

func TestToolReplayUsesStoredEnvelopeAndRunningStatus(t *testing.T) {
	trc := &toolRunContext{ToolRunID: "tool-1", Existing: &store.AIToolRun{
		ToolRunID: "tool-1", Status: "success", Result: json.RawMessage(`{"points":[1]}`),
		ResultQuality: "complete", ResultCount: 1, ResultDigestSHA256: "stored-digest",
		ResultTruncated: true,
	}}
	env := toolReplayEnvelope(trc)
	if env.Quality != "complete" || env.ToolRunID != "tool-1" || !env.Truncated || env.Digest != "stored-digest" {
		t.Fatalf("stored tool result was not replayed: %+v", env)
	}
	running := &toolRunContext{ToolRunID: "tool-2", Existing: &store.AIToolRun{ToolRunID: "tool-2", Status: "running"}}
	if got := toolReplayEnvelope(running); got.Quality != "partial" || len(got.SourceErrors) != 1 {
		t.Fatalf("running replay must be an explicit in-flight envelope: %+v", got)
	}
}
