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

func TestHugeGraphClientListVerticesForScopeUsesIndexedOffsetQuery(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("label") != "Entity" || r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("limit") != "5000" {
			t.Fatalf("unexpected pagination query: %s", r.URL.RawQuery)
		}
		var properties map[string]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("properties")), &properties); err != nil {
			t.Fatalf("properties: %v", err)
		}
		if properties["source"] != "kubernetes" || properties["tenant_id"] != "tenant" || properties["cluster_id"] != "cluster" {
			t.Fatalf("scope properties: %#v", properties)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vertices":[{"id":"v1"}]}`))
	}))
	defer server.Close()
	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.ListVerticesForScope(t.Context(), "kubernetes", "tenant", "cluster")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || requests != 1 || items[0]["id"] != "v1" {
		t.Fatalf("items=%#v requests=%d", items, requests)
	}
}

func TestHugeGraphClientListEdgesForScopeUsesFrozenLabelIndexes(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("limit") != "5000" {
			t.Fatalf("unexpected edge pagination query: %s", r.URL.RawQuery)
		}
		label := r.URL.Query().Get("label")
		if label == "" {
			t.Fatal("edge label is required")
		}
		seen[label] = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"edges":[]}`))
	}))
	defer server.Close()
	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.ListEdgesForScope(t.Context(), "kubernetes", "tenant", "cluster")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || len(seen) != len(RelationTypes()) {
		t.Fatalf("items=%#v labels=%d want=%d", items, len(seen), len(RelationTypes()))
	}
}

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

func TestHugeGraphClientVerifiesCreatedGraph(t *testing.T) {
	var createCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/graphspaces/DEFAULT/graphs":
			_, _ = w.Write([]byte(`{"graphs":["hugegraph"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphspaces/DEFAULT/graphs/aiops":
			createCalls++
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/graphspaces/DEFAULT/graphs/aiops":
			http.Error(w, "named graph is not loaded", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "admin", "secret", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = client.EnsureGraph(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("EnsureGraph() error = %v, want named graph verification failure", err)
	}
	if createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", createCalls)
	}
}

func TestHugeGraphClientVerifiesConfiguredGraph(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"name":"aiops","backend":"rocksdb"}`))
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "admin", "secret", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyGraph(context.Background()); err != nil {
		t.Fatalf("VerifyGraph() error = %v", err)
	}
}

func TestHugeGraphClientKNeighborUsesHugeGraph17AdvancedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops/traversers/kneighbor" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["source"] != "service:tenant-a:checkout" || body["direction"] != nil || body["edge_labels"] != nil {
			t.Fatalf("invalid top-level payload: %#v", body)
		}
		steps, ok := body["steps"].(map[string]interface{})
		if !ok || steps["direction"] != "OUT" || steps["max_degree"] != float64(500) {
			t.Fatalf("steps = %#v", body["steps"])
		}
		edgeSteps := steps["edge_steps"].([]interface{})
		if len(edgeSteps) != 2 || edgeSteps[0].(map[string]interface{})["label"] != "DEPENDS_ON" || edgeSteps[1].(map[string]interface{})["label"] != "CALLS" {
			t.Fatalf("edge_steps = %#v", edgeSteps)
		}
		if body["max_depth"] != float64(3) || body["nearest"] != nil || body["with_path"] != true || body["with_edge"] != true {
			t.Fatalf("traversal options = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vertices":[],"edges":[]}`))
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.KNeighbor(context.Background(), KNeighborRequest{
		Source: "service:tenant-a:checkout", Direction: "OUT", MaxDepth: 3, Limit: 100,
		Capacity: 500, Nearest: true, WithVertex: true, WithPath: true, WithEdge: true,
		EdgeLabels: []string{"DEPENDS_ON", "CALLS"},
	})
	if err != nil {
		t.Fatalf("KNeighbor returned error: %v", err)
	}
}

func TestHugeGraphClientShortestPathUsesQuotedGETQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops/traversers/shortestpath" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("source"); got != `"service:tenant-a:checkout"` {
			t.Fatalf("source = %q", got)
		}
		if got := r.URL.Query().Get("target"); got != `"service:tenant-a:payments"` {
			t.Fatalf("target = %q", got)
		}
		if got := r.URL.Query().Get("label"); got != "CALLS" || r.URL.Query().Get("direction") != "BOTH" || r.URL.Query().Get("max_depth") != "6" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		if r.Body != http.NoBody {
			t.Fatalf("GET request unexpectedly has a body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"path":["service:tenant-a:checkout","service:tenant-a:payments"]}`))
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ShortestPath(context.Background(), "service:tenant-a:checkout", "service:tenant-a:payments", 6, []string{"CALLS"})
	if err != nil {
		t.Fatalf("ShortestPath returned error: %v", err)
	}
	if len(traverserPathIDs(result)) != 2 {
		t.Fatalf("path = %#v", result["path"])
	}
}

func TestHugeGraphClientEdgesBetweenFiltersEndpointsAndLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops/graph/edges" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("vertex_id") != `"source:1"` || r.URL.Query().Get("direction") != "BOTH" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"edges":[
			{"id":"e1","label":"CALLS","outV":"source:1","inV":"target:1","properties":{"edge_uid":"e1"}},
			{"id":"e2","label":"OWNS","outV":"source:1","inV":"target:1","properties":{"edge_uid":"e2"}},
			{"id":"e3","label":"CALLS","outV":"source:1","inV":"other:1","properties":{"edge_uid":"e3"}}
		]}`))
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := client.EdgesBetween(context.Background(), "source:1", "target:1", []string{"CALLS", "OWNS"})
	if err != nil {
		t.Fatalf("EdgesBetween returned error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("edges = %#v", edges)
	}
}

func TestHugeGraphClientEdgesForVertexUsesQuotedCustomID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/graphspaces/DEFAULT/graphs/aiops/graph/edges" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("vertex_id") != `"service:tenant-a:checkout"` || r.URL.Query().Get("direction") != "BOTH" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		if r.URL.Query().Get("label") != "DEPENDS_ON" {
			t.Fatalf("label = %q", r.URL.Query().Get("label"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"edges":[{"id":"e1","label":"DEPENDS_ON","outV":"service:tenant-a:checkout","inV":"service:tenant-a:payments"}]}`))
	}))
	defer server.Close()

	client, err := NewHugeGraphClient(server.URL, "DEFAULT", "aiops", "", "", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := client.EdgesForVertex(context.Background(), "service:tenant-a:checkout", "BOTH", []string{"DEPENDS_ON"})
	if err != nil {
		t.Fatalf("EdgesForVertex returned error: %v", err)
	}
	if len(edges) != 1 || edges[0]["id"] != "e1" {
		t.Fatalf("edges = %#v", edges)
	}
}
