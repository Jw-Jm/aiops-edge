package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHugeGraphClientBatchVertexCarriesCustomStringIDAndEntityLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops/graph/vertices/batch" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("create_if_not_exist"); got != "true" || r.URL.Query().Get("update_strategies") != "OVERRIDE" {
			t.Fatalf("batch query = %v, want create_if_not_exist=true and update_strategies=OVERRIDE", r.URL.Query())
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		vertices, ok := body["vertices"].([]interface{})
		if !ok || len(vertices) != 1 {
			t.Fatalf("batch body = %#v", body)
		}
		vertex := vertices[0].(map[string]interface{})
		if vertex["id"] != "pod:v1:cluster-1:pod-1" || vertex["label"] != "Entity" {
			t.Fatalf("vertex body = %#v", vertex)
		}
		properties := vertex["properties"].(map[string]interface{})
		strategies, ok := body["update_strategies"].(map[string]interface{})
		if body["create_if_not_exist"] != true || !ok || strategies["entity_uid"] != "OVERRIDE" || strategies["attrs_json"] != "OVERRIDE" {
			t.Fatalf("batch options = %#v", body)
		}
		if properties["attrs_json"] != "{}" {
			t.Fatalf("attrs_json = %#v, want JSON text", properties["attrs_json"])
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", 500*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	err = client.PutVerticesBatch(context.Background(), []Entity{{EntityUID: "pod:v1:cluster-1:pod-1", EntityType: "pod"}})
	if err != nil {
		t.Fatalf("PutVerticesBatch returned error: %v", err)
	}
}

func TestHugeGraphClientQuotesStringVertexID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.EscapedPath(), "%22") || strings.Contains(r.URL.EscapedPath(), ":") {
			t.Fatalf("vertex UID was not escaped: %s", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pod:v1:cluster-1:pod-1","label":"Entity"}`))
	}))
	defer server.Close()
	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetVertex(context.Background(), "pod:v1:cluster-1:pod-1"); err != nil {
		t.Fatalf("GetVertex returned error: %v", err)
	}
}

func TestHugeGraphClientBoundsEdgeMutationBatches(t *testing.T) {
	calls, edgesSeen := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops/graph/edges/batch" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Edges []map[string]interface{} `json:"edges"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		calls++
		edgesSeen += len(body.Edges)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	edges := make([]Edge, 100)
	for i := range edges {
		edges[i] = Edge{
			SourceUID: "source:" + strings.Repeat("s", 100), TargetUID: "target:" + strings.Repeat("t", 100),
			RelationType: "OWNS",
		}
	}
	if err := client.PutEdgesBatch(context.Background(), edges); err != nil {
		t.Fatalf("PutEdgesBatch returned error: %v", err)
	}
	if calls < 2 || edgesSeen != len(edges) {
		t.Fatalf("physical batches = %d, edges seen = %d; want multiple batches containing all edges", calls, edgesSeen)
	}
}

func TestHugeGraphClientSeparatesReadAndWriteTimeouts(t *testing.T) {
	client, err := NewHugeGraphClient("http://127.0.0.1", "DEFAULT", "aiops", "", "", 11*time.Millisecond, 29*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if client.readClient.Timeout != 11*time.Millisecond || client.writeClient.Timeout != 29*time.Millisecond {
		t.Fatalf("timeouts = %v/%v", client.readClient.Timeout, client.writeClient.Timeout)
	}
}

func TestHugeGraphClientRejectsRawGraphLanguage(t *testing.T) {
	client, err := NewHugeGraphClient("http://127.0.0.1", "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RawQuery(context.Background(), "g.V()"); err == nil {
		t.Fatal("RawQuery accepted arbitrary graph language")
	}
}

func TestHugeGraphClientEnsuresNamedGraphBeforeSchemaMigration(t *testing.T) {
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/graphspaces/DEFAULT/graphs":
			w.Header().Set("Content-Type", "application/json")
			if created {
				_, _ = w.Write([]byte(`{"graphs":["aiops"]}`))
			} else {
				_, _ = w.Write([]byte(`{"graphs":[]}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/graphspaces/DEFAULT/graphs/aiops":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graph create body: %v", err)
			}
			if body["backend"] != "rocksdb" || body["rocksdb.data_path"] != "/var/lib/hugegraph/data/aiops" || body["rocksdb.wal_path"] != "/var/lib/hugegraph/wal/aiops" {
				t.Fatalf("graph create body = %#v", body)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/graphspaces/DEFAULT/graphs/aiops" && created:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"aiops","backend":"rocksdb"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "admin", "secret", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureGraph(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureGraph(context.Background()); err != nil {
		t.Fatal(err)
	}
}
