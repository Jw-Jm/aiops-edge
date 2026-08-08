package api

import (
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestValidateTopologyNodeType(t *testing.T) {
	if msg := validateTopologyNodeType(&store.TopologyNodeType{}); msg == "" {
		t.Fatal("expected empty name to fail")
	}
	if msg := validateTopologyNodeType(&store.TopologyNodeType{Name: "vm", Tier: 0}); msg != "" {
		t.Fatalf("expected valid, got %q", msg)
	}
	if msg := validateTopologyNodeType(&store.TopologyNodeType{Name: "vm", Tier: -1}); msg == "" {
		t.Fatal("expected negative tier to fail")
	}
}

func TestValidateTopologyRelationType(t *testing.T) {
	if msg := validateTopologyRelationType(&store.TopologyRelationType{}); msg == "" {
		t.Fatal("expected empty name to fail")
	}
	valid := &store.TopologyRelationType{
		Name:         "replicates_to",
		Direction:    "bidirectional",
		SemanticsTag: "redundancy",
	}
	if msg := validateTopologyRelationType(valid); msg != "" {
		t.Fatalf("expected valid, got %q", msg)
	}
	// 非法 direction
	badDir := *valid
	badDir.Direction = "sideways"
	if msg := validateTopologyRelationType(&badDir); msg == "" {
		t.Fatal("expected invalid direction to fail")
	}
	// 非法 semantics_tag
	badTag := *valid
	badTag.SemanticsTag = "magic"
	if msg := validateTopologyRelationType(&badTag); msg == "" {
		t.Fatal("expected invalid semantics_tag to fail")
	}
}

func TestKnownDirectionsAndTags(t *testing.T) {
	for d := range validDirections {
		if d != "src_to_dst" && d != "dst_to_src" && d != "bidirectional" {
			t.Fatalf("unexpected direction %s", d)
		}
	}
	for s := range validSemanticsTags {
		if s == "" {
			t.Fatal("empty semantics tag")
		}
	}
}
