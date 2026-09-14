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

func TestPropagationPolicyCoversKubeVirtDependencyRelations(t *testing.T) {
	for _, relation := range []string{"USES_DISK", "REFERENCES_VOLUME", "SOURCED_FROM", "DECLARES", "CONNECTS_TO_NAD", "USES_CNI"} {
		if got := CandidateDirection(relation); got != "OUT" {
			t.Fatalf("CandidateDirection(%s) = %q, want OUT", relation, got)
		}
		if got := ImpactDirection(relation); got != "IN" {
			t.Fatalf("ImpactDirection(%s) = %q, want IN", relation, got)
		}
	}
}
