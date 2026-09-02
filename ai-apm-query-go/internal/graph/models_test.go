package graph

import (
	"encoding/json"
	"testing"
)

func TestGraphDTOUsesStrictV1Fields(t *testing.T) {
	entity := Entity{EntityUID: "pod-1", EntityType: "pod", TenantID: "tenant-1", ClusterID: "cluster-1", Name: "pod"}
	raw, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}
	if string(raw) == "" || string(raw) == "null" {
		t.Fatalf("marshal entity returned %s", raw)
	}
	var decoded Entity
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}
	if decoded.EntityUID != entity.EntityUID || decoded.EntityType != entity.EntityType {
		t.Fatalf("round trip entity = %+v, want %+v", decoded, entity)
	}
}

func TestGraphMetaCarriesNewestProjectionGeneration(t *testing.T) {
	vertices := []Entity{
		{EntityUID: "v1", Generation: 3},
		{EntityUID: "v2", Generation: 8},
	}
	edges := []Edge{{EdgeUID: "e1", Generation: 7}}
	meta := graphMeta(vertices, edges, false, nil, "2026-09-02T00:00:00Z")
	if meta.GraphGeneration != 8 {
		t.Fatalf("graph generation=%d, want newest generation 8", meta.GraphGeneration)
	}
	if meta.Partial {
		t.Fatal("complete graph unexpectedly marked partial")
	}
}

func TestGraphMetaUsesEdgeGenerationWhenVerticesHaveNoGeneration(t *testing.T) {
	meta := graphMeta([]Entity{{EntityUID: "v1"}}, []Edge{{EdgeUID: "e1", Generation: 11}}, true,
		[]string{ErrGraphQueryLimitExceeded}, "2026-09-02T00:00:00Z")
	if meta.GraphGeneration != 11 {
		t.Fatalf("graph generation=%d, want edge generation 11", meta.GraphGeneration)
	}
	if !meta.Partial || len(meta.WarningCodes) != 1 {
		t.Fatalf("partial graph metadata lost: %+v", meta)
	}
}
