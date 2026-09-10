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
		_, _ = w.Write([]byte(`{"ids":[["allowed","wrong","missing"]],"distances":[[0.1,0.2,0.3]],"metadatas":[[{"tenant_id":"tenant-a","scope_type":"cluster","cluster_id":"cluster-a","knowledge_id":"k-1","version_id":"v-2","status":"published","is_current":true,"source":"runbook"},{"tenant_id":"tenant-b","scope_type":"cluster","cluster_id":"cluster-a","knowledge_id":"k-2","version_id":"v-2","status":"published","is_current":true,"source":"secret"},{}]],"documents":[["a","b","c"]]}`))
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
	if !ok || len(clauses) != 4 {
		t.Fatalf("where.$and = %#v", where["$and"])
	}
	if !containsKnowledgeScopeClause(clauses, "tenant_id", scope.TenantID) ||
		!containsKnowledgeScopeClause(clauses, "status", "published") {
		t.Fatalf("where clauses = %#v", clauses)
	}
	if !containsKnowledgeScopeClause(clauses, "is_current", true) {
		t.Fatalf("where clauses = %#v", clauses)
	}
	if !containsKnowledgeScopeOrClause(clauses, scope) {
		t.Fatalf("where scope clause = %#v", clauses)
	}
}

func containsKnowledgeScopeClause(clauses []interface{}, key string, want interface{}) bool {
	for _, raw := range clauses {
		clause, ok := raw.(map[string]interface{})
		if ok && clause[key] == want {
			return true
		}
	}
	return false
}

func containsKnowledgeScopeOrClause(clauses []interface{}, scope query.KnowledgeScope) bool {
	for _, raw := range clauses {
		clause, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		or, ok := clause["$or"].([]interface{})
		if !ok || len(or) != 2 {
			continue
		}
		platform, platformOK := or[0].(map[string]interface{})
		cluster, clusterOK := or[1].(map[string]interface{})
		if platformOK && clusterOK && platform["scope_type"] == "platform_common" &&
			cluster["scope_type"] == "cluster" && cluster["cluster_id"] == scope.ClusterID {
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
			{"tenant_id": "tenant-a", "scope_type": "cluster", "cluster_id": "cluster-a", "knowledge_id": "k-1", "version_id": "v-1", "status": "published", "is_current": true},
			{"tenant_id": "tenant-b", "scope_type": "cluster", "cluster_id": "cluster-a", "knowledge_id": "k-2", "version_id": "v-1", "status": "published", "is_current": true},
			{},
		}},
	}
	hits := mapChromaHits(cr, scope)
	if len(hits) != 1 || hits[0].DocumentID != "same" {
		t.Fatalf("hits = %+v", hits)
	}
}
