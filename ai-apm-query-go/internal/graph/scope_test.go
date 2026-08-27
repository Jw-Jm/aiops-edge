package graph

import "testing"

func TestScopeAllowsGlobalVertexOnlyWhenClusterIsEmpty(t *testing.T) {
	scope := GraphScope{TenantID: "tenant-a", ClusterIDs: map[string]struct{}{"cluster-a": {}}}
	if !scope.Allows(Entity{TenantID: "tenant-a", ClusterID: "cluster-a"}) {
		t.Fatal("authorized cluster was rejected")
	}
	if scope.Allows(Entity{TenantID: "tenant-a", ClusterID: "cluster-b"}) {
		t.Fatal("unauthorized cluster was allowed")
	}
	if !scope.Allows(Entity{TenantID: "tenant-a", ClusterID: ""}) {
		t.Fatal("global entity should be allowed")
	}
}
