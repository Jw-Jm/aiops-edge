package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestDeriveRunRootCauseUsesEvidenceConfirmedHighestConfidence(t *testing.T) {
	rootCause, confidence := deriveRunRootCause([]store.AIHypothesis{
		{Content: "weak signal", Confidence: 0.95, ConfirmedByEvidence: false},
		{Content: "database saturation", Confidence: 0.82, ConfirmedByEvidence: true},
		{Content: "network jitter", Confidence: 0.61, ConfirmedByEvidence: true},
	})
	if rootCause != "database saturation" || confidence != 0.82 {
		t.Fatalf("projection=(%q,%v), want evidence-confirmed highest confidence", rootCause, confidence)
	}
}

func TestProjectInvestigationSummaryRoundTrip(t *testing.T) {
	metadata := []byte(`{"investigation_summary":{"schema_version":1,"termination_reason":"budget_exhausted","budget_summary":{"max_steps":10,"max_tools":16,"consumed_steps":10,"consumed_tools":16},"evidence_ids":["ev-1"]}}`)
	raw, ok := projectInvestigationSummary(metadata)
	if !ok {
		t.Fatal("valid metadata must project a summary")
	}
	var summary struct {
		TerminationReason string   `json:"termination_reason"`
		EvidenceIDs       []string `json:"evidence_ids"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.TerminationReason != "budget_exhausted" {
		t.Fatalf("termination = %s", summary.TerminationReason)
	}
	if len(summary.EvidenceIDs) != 1 || summary.EvidenceIDs[0] != "ev-1" {
		t.Fatalf("evidence = %v", summary.EvidenceIDs)
	}
}

func TestProjectInvestigationSummaryFailsClosedOnInvalidJSON(t *testing.T) {
	if _, ok := projectInvestigationSummary([]byte(`{"investigation_summary":"not-an-object"`)); ok {
		t.Fatal("truncated JSON must not project a summary")
	}
	if _, ok := projectInvestigationSummary([]byte(`{"other":1}`)); ok {
		t.Fatal("metadata without a summary must not fabricate one")
	}
	if _, ok := projectInvestigationSummary(nil); ok {
		t.Fatal("empty metadata must not fabricate a summary")
	}
}

func TestExtractInvestigationSummaryFromTerminalResult(t *testing.T) {
	raw := extractInvestigationSummary([]byte(`{"status":"partial","investigation_summary":{"termination_reason":"budget_exhausted"}}`))
	if raw == nil {
		t.Fatal("terminal result with a summary must be persisted")
	}
	if !strings.Contains(string(raw), "budget_exhausted") {
		t.Fatalf("extracted = %s", raw)
	}
	if extractInvestigationSummary([]byte(`{"status":"created"}`)) != nil {
		t.Fatal("non-summary results must not gain a fabricated summary")
	}
}
