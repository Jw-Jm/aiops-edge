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
