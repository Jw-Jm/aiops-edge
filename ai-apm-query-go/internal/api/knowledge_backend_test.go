package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

func TestChromaKnowledgeBackendScopesRequestAndResponse(t *testing.T) {
	scope := query.KnowledgeScope{TenantID: "tenant-a", ClusterID: "cluster-a"}
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ids":[["allowed","wrong","missing"]],"distances":[[0.1,0.2,0.3]],"metadatas":[[{"tenant_id":"tenant-a","cluster_id":"cluster-a","source":"runbook"},{"tenant_id":"tenant-b","cluster_id":"cluster-a","source":"secret"},{}]],"documents":[["a","b","c"]]}`))
	}))
	defer srv.Close()

	b := chromaKnowledgeBackend{cfg: knowledgeBackendCfg{
		chromaURL:  srv.URL,
		collection: "aiops-knowledge",
		client:     &http.Client{Timeout: time.Second},
	}}
	hits, err := b.Search(context.Background(), scope, "pod crashloop", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].DocumentID != "allowed" {
		t.Fatalf("hits = %+v, want only scope-matching hit", hits)
	}
	where, ok := got["where"].(map[string]interface{})
	if !ok {
		t.Fatalf("where missing from request: %+v", got)
	}
	clauses, ok := where["$and"].([]interface{})
	if !ok || len(clauses) != 2 {
		t.Fatalf("where.$and = %#v", where["$and"])
	}
	if !containsKnowledgeScopeClause(clauses, "tenant_id", scope.TenantID) ||
		!containsKnowledgeScopeClause(clauses, "cluster_id", scope.ClusterID) {
		t.Fatalf("where clauses = %#v", clauses)
	}
}

func containsKnowledgeScopeClause(clauses []interface{}, key, want string) bool {
	for _, raw := range clauses {
		clause, ok := raw.(map[string]interface{})
		if ok && clause[key] == want {
			return true
		}
	}
	return false
}

func TestChromaKnowledgeBackendUsesBoundedTimeout(t *testing.T) {
	t.Setenv("CHROMA_URL", "http://chroma.invalid")
	t.Setenv("CHROMA_COLLECTION", "knowledge")
	backend, ok := newKnowledgeBackendFromEnv().(chromaKnowledgeBackend)
	if !ok {
		t.Fatalf("backend type = %T", newKnowledgeBackendFromEnv())
	}
	if got, want := backend.cfg.client.Timeout, 15*time.Second; got != want {
		t.Fatalf("timeout = %s, want %s", got, want)
	}
}

func TestMapChromaHitsRejectsMissingOrForeignScope(t *testing.T) {
	scope := query.KnowledgeScope{TenantID: "tenant-a", ClusterID: "cluster-a"}
	cr := &chromaQueryResponse{
		IDs:       [][]string{{"same", "foreign", "missing"}},
		Distances: [][]float64{{0, 0, 0}},
		Metadatas: []([]map[string]interface{}){{
			{"tenant_id": "tenant-a", "cluster_id": "cluster-a"},
			{"tenant_id": "tenant-b", "cluster_id": "cluster-a"},
			{},
		}},
	}
	hits := mapChromaHits(cr, scope)
	if len(hits) != 1 || hits[0].DocumentID != "same" {
		t.Fatalf("hits = %+v", hits)
	}
}
