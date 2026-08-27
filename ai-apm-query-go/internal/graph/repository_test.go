package graph

import (
	"context"
	"testing"
)

func TestMemoryRepositoryEnforcesTenantScopeOnEntityAndEdge(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.BatchMutate(context.Background(), MutationBatch{
		TenantID: "tenant-a", ClusterID: "cluster-a", Vertices: []Entity{
			{EntityUID: "a", EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Status: "active"},
			{EntityUID: "b", EntityType: "middleware", TenantID: "tenant-a", ClusterID: "cluster-a", Status: "active"},
		}, Edges: []Edge{{EdgeUID: "e", SourceUID: "a", TargetUID: "b", RelationType: "DEPENDS_ON", TenantID: "tenant-a", ClusterID: "cluster-a", Status: "active"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetEntity(context.Background(), GraphScope{TenantID: "tenant-b"}, "a"); err == nil {
		t.Fatal("GetEntity leaked an entity across tenants")
	}
	if _, err := repo.Neighbors(context.Background(), GraphScope{TenantID: "tenant-b"}, NeighborQuery{CenterEntityUID: "a", MaxDepth: 1}); err == nil {
		t.Fatal("Neighbors leaked a cross-tenant graph")
	}
}

func TestRepositoryRejectsTraversalAboveServerLimit(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.Neighbors(context.Background(), GraphScope{TenantID: "tenant-a"}, NeighborQuery{CenterEntityUID: "a", MaxDepth: 7})
	if err == nil {
		t.Fatal("Neighbors accepted depth above internal limit")
	}
	if graphErr, ok := err.(*Error); !ok || graphErr.Code != ErrGraphQueryLimitExceeded {
		t.Fatalf("error = %v, want %s", err, ErrGraphQueryLimitExceeded)
	}
}
