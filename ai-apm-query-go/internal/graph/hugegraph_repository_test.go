package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHugeGraphRepositoryFallsBackToIndexedEdgesWhenKNeighborIsEmpty(t *testing.T) {
	centerUID := "service:v1:tenant-a:center"
	targetUID := "service:v1:tenant-a:target"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphspaces/DEFAULT/graphs/aiops/traversers/kneighbor":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vertices":[],"edges":[],"paths":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/graphspaces/DEFAULT/graphs/aiops/graph/edges":
			if r.URL.Query().Get("vertex_id") != `"service:v1:tenant-a:center"` || r.URL.Query().Get("label") != "DEPENDS_ON" {
				t.Fatalf("edge query = %v", r.URL.Query())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"edges":[{"id":"edge-1","label":"DEPENDS_ON","outV":"service:v1:tenant-a:center","inV":"service:v1:tenant-a:target","properties":{"edge_uid":"edge-1","tenant_id":"tenant-a","cluster_id":"cluster-a","status":"active"}}]}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.EscapedPath(), "/graph/vertices/"):
			uid := ""
			switch {
			case strings.Contains(r.URL.EscapedPath(), "center"):
				uid = centerUID
			case strings.Contains(r.URL.EscapedPath(), "target"):
				uid = targetUID
			}
			if uid == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q,"label":"Entity","properties":{"entity_uid":%q,"entity_type":"service","tenant_id":"tenant-a","cluster_id":"cluster-a","name":%q,"name_key":%q,"source":"trace","status":"active"}}`, uid, uid, uid, uid)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewHugeGraphRepository(client)
	result, err := repo.Neighbors(context.Background(), GraphScope{TenantID: "tenant-a", ClusterIDs: map[string]struct{}{"cluster-a": {}}}, NeighborQuery{
		CenterEntityUID: centerUID, Direction: "BOTH", MaxDepth: 1, MaxVertices: 10, MaxEdges: 10, RelationTypes: []string{"DEPENDS_ON"},
	})
	if err != nil {
		t.Fatalf("Neighbors returned error: %v", err)
	}
	if len(result.Vertices) != 2 || len(result.Edges) != 1 || result.Edges[0].RelationType != "DEPENDS_ON" {
		t.Fatalf("subgraph = %+v", result)
	}
}
