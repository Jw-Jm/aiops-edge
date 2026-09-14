package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
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

	candidate := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/service:v1:tenant-a:service/candidate?depth=2", nil)
	candidate = withAuthorizationContext(candidate, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session"})
	candidate.Header.Set("X-Cluster-ID", "cluster-a")
	candidateRec := httptest.NewRecorder()
	h.GraphPublicRouter(candidateRec, candidate)
	if candidateRec.Code != http.StatusOK {
		t.Fatalf("candidate status=%d body=%s", candidateRec.Code, candidateRec.Body.String())
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

// TestGraphSearchOperationsProfileExcludesCompatibilityAndDetailOnlyTypes 锁定默认图谱
// 搜索边界：operations profile 不得泄漏 ReplicaSet、业务 service、middleware 等
// 兼容/仅详情类型，但必须保留 Pod/PVC/VMI 这类真实载体。
func TestGraphSearchOperationsProfileExcludesCompatibilityAndDetailOnlyTypes(t *testing.T) {
	repo := graphpkg.NewMemoryRepository()
	types := []string{"pod", "vmi", "pvc", "replicaset", "service", "middleware"}
	vertices := make([]graphpkg.Entity, 0, len(types))
	for _, entityType := range types {
		vertices = append(vertices, graphpkg.Entity{
			EntityUID: "entity:" + entityType, EntityType: entityType,
			TenantID: "tenant-a", ClusterID: "cluster-a",
			Name: "item-" + entityType, NameKey: "item-" + entityType,
			Source: "test", Status: "active",
		})
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Vertices: vertices}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{graphRepo: repo}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/search?q=item&profile=operations&limit=20", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session", ActiveClusterID: "cluster-a"})
	rec := httptest.NewRecorder()
	h.GraphPublicRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []graphpkg.Entity `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(body.Items))
	for _, item := range body.Items {
		got = append(got, item.EntityType)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"pod", "pvc", "vmi"}) {
		t.Fatalf("types=%v", got)
	}
}

// TestGraphSearchRejectsUnknownProfile 验证未知 profile 直接拒绝，而不是退回全类型搜索。
func TestGraphSearchRejectsUnknownProfile(t *testing.T) {
	h := graphTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/search?q=checkout&profile=everything", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session", ActiveClusterID: "cluster-a"})
	rec := httptest.NewRecorder()
	h.GraphPublicRouter(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown profile must be rejected, status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGraphSearchWithoutProfileKeepsCompatibilityReads 验证不传 profile 时兼容类型
// 仍可通过精确类型访问（详情/历史深链不受影响）。
func TestGraphSearchWithoutProfileKeepsCompatibilityReads(t *testing.T) {
	repo := graphpkg.NewMemoryRepository()
	vertices := []graphpkg.Entity{
		{EntityUID: "entity:replicaset", EntityType: "replicaset", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "item-rs", NameKey: "item-rs", Source: "test", Status: "active"},
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Vertices: vertices}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{graphRepo: repo}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/search?q=item&entity_type=replicaset", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session", ActiveClusterID: "cluster-a"})
	rec := httptest.NewRecorder()
	h.GraphPublicRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "replicaset") {
		t.Fatalf("compatibility type must stay readable without a profile: %s", rec.Body.String())
	}
}

func TestGraphPublicErrorDoesNotExposeBackendDiagnostics(t *testing.T) {
	for name, input := range map[string]error{
		"typed":   graphpkg.NewError(graphpkg.ErrGraphUnavailable, "HugeGraph HTTP 500 https://internal.example/?token=secret-token"),
		"wrapped": errors.New("mysql://user:password@internal.example graph backend failed"),
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondGraphErrorFromGo(rec, input)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, "secret-token") || strings.Contains(body, "password") || strings.Contains(body, "internal.example") {
				t.Fatalf("backend diagnostic leaked: %s", body)
			}
			if !strings.Contains(body, "knowledge graph is unavailable") {
				t.Fatalf("generic graph error missing: %s", body)
			}
		})
	}
}

func TestGraphPublicNeighborsReturnsBudgetMetadataAndCursor(t *testing.T) {
	repo := graphpkg.NewMemoryRepository()
	center := graphpkg.Entity{EntityUID: "service:v1:tenant-a:center", EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "center", NameKey: "center", Source: "catalog", Status: "active"}
	vertices := []graphpkg.Entity{center}
	edges := make([]graphpkg.Edge, 0, 80)
	for index := 0; index < 80; index++ {
		vertex := graphpkg.Entity{EntityUID: "service:v1:tenant-a/node-" + strconv.Itoa(index), EntityType: "service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "node-" + strconv.Itoa(index), NameKey: "node-" + strconv.Itoa(index), Source: "catalog", Status: "active"}
		vertices = append(vertices, vertex)
		edges = append(edges, graphpkg.Edge{EdgeUID: "edge:v1:" + strconv.Itoa(index), SourceUID: center.EntityUID, TargetUID: vertex.EntityUID, RelationType: "DEPENDS_ON", TenantID: "tenant-a", ClusterID: "cluster-a", Source: "catalog", Status: "active", AttrsVersion: 1})
	}
	repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Vertices: vertices, Edges: edges})
	h := &Handler{graphRepo: repo}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/"+url.PathEscape(center.EntityUID)+"/neighbors?depth=1&max_vertices=80&max_edges=200", nil)
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
	if len(body.Vertices) > 80 || len(body.Edges) > 200 {
		t.Fatalf("visible graph exceeds budget: vertices=%d edges=%d", len(body.Vertices), len(body.Edges))
	}
	if body.TotalNodes <= len(body.Vertices) || body.TotalEdges <= len(body.Edges) || body.NextCursor == "" || !body.Aggregated {
		t.Fatalf("budget metadata=%+v", body)
	}
}
