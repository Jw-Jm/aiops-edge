package api

import (
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
