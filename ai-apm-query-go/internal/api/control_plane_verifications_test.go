package api

import (
	"encoding/json"
	"testing"
)

func TestDeriveVerificationStatusRequiresEvidence(t *testing.T) {
	status, err := deriveVerificationStatus(json.RawMessage(`[]`))
	if err != nil || status != "inconclusive" {
		t.Fatalf("empty checks = %q, %v; want inconclusive", status, err)
	}
	status, err = deriveVerificationStatus(json.RawMessage(`[{"effect_size":1.2,"side_effect":false}]`))
	if err != nil || status != "passed" {
		t.Fatalf("positive effect = %q, %v; want passed", status, err)
	}
}

func TestDeriveVerificationStatusDetectsRegressionBeforePass(t *testing.T) {
	status, err := deriveVerificationStatus(json.RawMessage(`[{"effect_size":2,"side_effect":true}]`))
	if err != nil || status != "regressed" {
		t.Fatalf("side effect = %q, %v; want regressed", status, err)
	}
	status, err = deriveVerificationStatus(json.RawMessage(`[{"effect_size":0,"side_effect":false}]`))
	if err != nil || status != "failed" {
		t.Fatalf("non-positive effect = %q, %v; want failed", status, err)
	}
}
