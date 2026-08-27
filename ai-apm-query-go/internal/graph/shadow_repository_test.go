package graph

import (
	"context"
	"errors"
	"testing"
)

type failingGraphRepository struct{}

func (failingGraphRepository) GetEntity(context.Context, GraphScope, string) (Entity, error) {
	return Entity{}, errors.New("secondary unavailable")
}
func (failingGraphRepository) SearchEntities(context.Context, GraphScope, EntitySearchQuery) ([]Entity, error) {
	return nil, errors.New("secondary unavailable")
}
func (failingGraphRepository) Neighbors(context.Context, GraphScope, NeighborQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("secondary unavailable")
}
func (failingGraphRepository) ShortestPath(context.Context, GraphScope, PathQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("secondary unavailable")
}
func (failingGraphRepository) Impact(context.Context, GraphScope, ImpactQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("secondary unavailable")
}
func (failingGraphRepository) CandidateSubgraph(context.Context, GraphScope, NeighborQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("secondary unavailable")
}
func (failingGraphRepository) BatchMutate(context.Context, MutationBatch) (MutationResult, error) {
	return MutationResult{}, errors.New("secondary unavailable")
}
func (failingGraphRepository) Health(context.Context) GraphHealth {
	return GraphHealth{Ready: false, Backend: "hugegraph"}
}

func TestShadowRepositoryReturnsLegacyResultWhenSecondaryIsDown(t *testing.T) {
	primary := NewMemoryRepository()
	entity := Entity{EntityUID: "service:v1:tenant-a:s", EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "checkout", NameKey: "checkout", Source: "catalog", Status: "active"}
	if _, err := primary.BatchMutate(context.Background(), MutationBatch{TenantID: "tenant-a", Vertices: []Entity{entity}}); err != nil {
		t.Fatal(err)
	}
	var diff ShadowDiff
	shadow := NewShadowRepository(primary, failingGraphRepository{}, func(value ShadowDiff) { diff = value })
	got, err := shadow.GetEntity(context.Background(), GraphScope{TenantID: "tenant-a", ClusterIDs: map[string]struct{}{"cluster-a": {}}}, entity.EntityUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EntityUID != entity.EntityUID || diff.SampleKind != "get_entity" || diff.MismatchCount != 1 {
		t.Fatalf("got=%+v diff=%+v", got, diff)
	}
}

func TestShadowRepositoryDoesNotLetSecondaryWriteFailureFailPrimary(t *testing.T) {
	primary := NewMemoryRepository()
	shadow := NewShadowRepository(primary, failingGraphRepository{}, nil)
	entity := Entity{EntityUID: "service:v1:tenant-a:s", EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "checkout", NameKey: "checkout", Source: "catalog", Status: "active"}
	result, err := shadow.BatchMutate(context.Background(), MutationBatch{TenantID: "tenant-a", Vertices: []Entity{entity}})
	if err != nil || result.Applied != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
