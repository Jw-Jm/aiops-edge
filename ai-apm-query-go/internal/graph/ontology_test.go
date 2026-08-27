package graph

import "testing"

func TestOntologyRejectsUnknownEntityType(t *testing.T) {
	if err := ValidateEntityType("not-an-entity"); err == nil {
		t.Fatal("ValidateEntityType accepted an unknown type")
	}
}

func TestOntologyAcceptsCanonicalKubernetesRelation(t *testing.T) {
	if err := ValidateRelation("RUNS_ON", "pod", "k8s_node"); err != nil {
		t.Fatalf("ValidateRelation returned error: %v", err)
	}
}

func TestOntologyRejectsNameBasedLegacyRelation(t *testing.T) {
	if err := ValidateRelation("CONNECTED_TO", "service", "service"); err == nil {
		t.Fatal("ValidateRelation accepted a retired relation")
	}
}

func TestPropagationPolicyUsesFrozenDirections(t *testing.T) {
	if got := CandidateDirection("RUNS_ON"); got != "OUT" {
		t.Fatalf("CandidateDirection(RUNS_ON) = %q, want OUT", got)
	}
	if got := ImpactDirection("RUNS_ON"); got != "IN" {
		t.Fatalf("ImpactDirection(RUNS_ON) = %q, want IN", got)
	}
}
