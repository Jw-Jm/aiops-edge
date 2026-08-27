package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHugeGraphRepositoryMapsVertexAndEnforcesScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/graphspaces/DEFAULT/graphs/aiops/graph/vertices/%22service%3Av1%3Atenant-a%3Asvc-1%22" {
			t.Fatalf("path = %q", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"service:v1:tenant-a:svc-1","label":"Entity","properties":{"entity_uid":"service:v1:tenant-a:svc-1","entity_type":"service","tenant_id":"tenant-a","cluster_id":"cluster-a","name":"Checkout","name_key":"checkout","source":"catalog","status":"active","confidence":1,"attrs_version":2}}`))
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewHugeGraphRepository(client)
	entity, err := repo.GetEntity(context.Background(), GraphScope{TenantID: "tenant-a", ClusterIDs: map[string]struct{}{"cluster-a": {}}}, "service:v1:tenant-a:svc-1")
	if err != nil {
		t.Fatal(err)
	}
	if entity.EntityType != "service" || entity.NameKey != "checkout" || entity.AttrsVersion != 2 {
		t.Fatalf("mapped entity = %+v", entity)
	}
	if _, err := repo.GetEntity(context.Background(), GraphScope{TenantID: "tenant-b"}, "service:v1:tenant-a:svc-1"); err == nil {
		t.Fatal("cross-tenant vertex was returned")
	}
}

func TestHugeGraphRepositoryRejectsRawQueryAndInvalidMutationBeforeWrite(t *testing.T) {
	client, err := NewHugeGraphClient("http://127.0.0.1:1", "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewHugeGraphRepository(client)
	if _, err := repo.RawQuery(context.Background(), "g.V()"); err == nil {
		t.Fatal("raw graph query was accepted")
	}
	_, err = repo.BatchMutate(context.Background(), MutationBatch{TenantID: "tenant-a", Vertices: []Entity{{EntityUID: "bad", EntityType: "not-a-type", TenantID: "tenant-a"}}})
	if err == nil {
		t.Fatal("invalid mutation was sent")
	}
}
