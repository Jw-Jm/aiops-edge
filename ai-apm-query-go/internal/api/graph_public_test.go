package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
)

func graphTestHandler(t *testing.T) *Handler {
	t.Helper()
	repo := graphpkg.NewMemoryRepository()
	vertices := []graphpkg.Entity{
		{EntityUID: "service:v1:tenant-a:service", EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "checkout", NameKey: "checkout", Source: "catalog", Status: "active"},
		{EntityUID: "service:v1:tenant-a:backend", EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "backend", NameKey: "backend", Source: "catalog", Status: "active"},
		{EntityUID: "service:v1:tenant-b:service", EntityType: "service", TenantID: "tenant-b", ClusterID: "cluster-b", Name: "secret", NameKey: "secret", Source: "catalog", Status: "active"},
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", Vertices: vertices[:2], Edges: []graphpkg.Edge{{EdgeUID: "edge:v1:service-backend", SourceUID: vertices[0].EntityUID, TargetUID: vertices[1].EntityUID, RelationType: "DEPENDS_ON", TenantID: "tenant-a", ClusterID: "cluster-a", Source: "catalog", Status: "active"}}}); err != nil {
		t.Fatal(err)
	}
	return &Handler{graphRepo: repo}
}

func withGraphAuth(r *http.Request, tenant, cluster string) *http.Request {
	return withAuthorizationContext(r, AuthorizationContext{UserID: "user", TenantID: tenant, SessionID: "session"}).WithContext(context.WithValue(r.Context(), graphTestClusterKey{}, cluster))
}

type graphTestClusterKey struct{}

func TestGraphPublicNeighborsReturnsTypedSubgraphAndEnforcesTenantScope(t *testing.T) {
	h := graphTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/service:v1:tenant-a:service/neighbors?depth=1", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session"})
	req.Header.Set("X-Cluster-ID", "cluster-a")
	rec := httptest.NewRecorder()
	h.GraphPublicRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body graphpkg.Subgraph
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Vertices) != 2 || len(body.Edges) != 1 {
		t.Fatalf("subgraph=%+v", body)
	}

	crossTenant := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/service:v1:tenant-a:service", nil)
	crossTenant = withAuthorizationContext(crossTenant, AuthorizationContext{UserID: "user", TenantID: "tenant-b", SessionID: "session"})
	crossTenant.Header.Set("X-Cluster-ID", "cluster-b")
	crossRec := httptest.NewRecorder()
	h.GraphPublicRouter(crossRec, crossTenant)
	if crossRec.Code != http.StatusForbidden && crossRec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status=%d body=%s", crossRec.Code, crossRec.Body.String())
	}
}

func TestGraphPublicRejectsRawGraphLanguageAndTraversalAboveLimit(t *testing.T) {
	h := graphTestHandler(t)
	raw := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/search?q=checkout&gremlin=g.V()", nil)
	raw = withAuthorizationContext(raw, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session"})
	raw.Header.Set("X-Cluster-ID", "cluster-a")
	rec := httptest.NewRecorder()
	h.GraphPublicRouter(rec, raw)
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadRequest {
		t.Fatalf("raw query status=%d body=%s", rec.Code, rec.Body.String())
	}

	tooDeep := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/service:v1:tenant-a:service/neighbors?depth=4", nil)
	tooDeep = withAuthorizationContext(tooDeep, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session"})
	tooDeep.Header.Set("X-Cluster-ID", "cluster-a")
	deepRec := httptest.NewRecorder()
	h.GraphPublicRouter(deepRec, tooDeep)
	if deepRec.Code != http.StatusBadRequest && deepRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("too deep status=%d body=%s", deepRec.Code, deepRec.Body.String())
	}
}
