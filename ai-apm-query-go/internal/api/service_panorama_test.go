package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

func servicePanoramaHandler(t *testing.T) *Handler {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		statement := r.URL.Query().Get("query")
		switch {
		case strings.Contains(statement, "quantile(0.95)"):
			_, _ = w.Write([]byte(`{"p95_ms":42.5}` + "\n"))
		case strings.Contains(statement, "FROM observability.service_topology"):
			_, _ = w.Write([]byte("{" + `"source_service":"checkout","target_service":"payments","calls":80,"errs":4,"avg_ns":38000000` + "}" + "\n"))
		case strings.Contains(statement, "k8s_namespace"):
			_, _ = w.Write([]byte("{" + `"service":"checkout","ns":"shop","calls":100` + "}" + "\n" + "{" + `"service":"payments","ns":"billing","calls":80` + "}" + "\n"))
		case strings.Contains(statement, "GROUP BY service_name"):
			_, _ = w.Write([]byte("{" + `"service":"checkout","calls":100,"errs":5,"avg_ns":12000000` + "}" + "\n" + "{" + `"service":"payments","calls":80,"errs":2,"avg_ns":22000000` + "}" + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	t.Cleanup(server.Close)
	host, port := splitHostPort(server.URL)
	h := &Handler{client: &http.Client{Timeout: 5 * time.Second}}
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.topoRepo = query.NewTopologyRepository(&h.repo)
	return h
}

func panoramaRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return withAuthorizationContext(req, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session"})
}

func decodePanoramaResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestServicePanoramaOverviewUsesDedicatedMetricsAndP95(t *testing.T) {
	h := servicePanoramaHandler(t)
	recorder := httptest.NewRecorder()
	h.ServicePanoramaOverview(recorder, panoramaRequest("/api/v1/services/overview?minutes=60"))
	body := decodePanoramaResponse(t, recorder)
	if body["total"] != float64(2) || body["calls"] != float64(180) || body["errors"] != float64(7) {
		t.Fatalf("unexpected overview: %v", body)
	}
	if body["p95_latency_ms"] != 42.5 {
		t.Fatalf("p95=%v, want 42.5", body["p95_latency_ms"])
	}
	if body["topology_revision"] == "" {
		t.Fatal("overview must expose a stable topology revision")
	}
}

func TestServicePanoramaMapGroupsAndAggregatesRoutes(t *testing.T) {
	h := servicePanoramaHandler(t)
	repo := graphpkg.NewMemoryRepository()
	_, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", Vertices: []graphpkg.Entity{
		{EntityUID: "service:checkout", EntityType: "service", TenantID: "tenant-a", Name: "checkout", NameKey: "checkout", Status: "active", Attrs: map[string]interface{}{"application_uid": "app:shop", "application_name": "shop"}},
		{EntityUID: "service:payments", EntityType: "service", TenantID: "tenant-a", Name: "payments", NameKey: "payments", Status: "active", Attrs: map[string]interface{}{"application_uid": "app:billing", "application_name": "billing"}},
	}, Edges: nil})
	if err != nil {
		t.Fatal(err)
	}
	h.graphRepo = repo
	recorder := httptest.NewRecorder()
	h.ServicePanoramaMap(recorder, panoramaRequest("/api/v1/services/map?group_by=application"))
	body := decodePanoramaResponse(t, recorder)
	if body["group_by"] != "application" {
		t.Fatalf("group_by=%v", body["group_by"])
	}
	groups, ok := body["groups"].([]interface{})
	if !ok || len(groups) != 2 {
		t.Fatalf("groups=%v", body["groups"])
	}
	edges, ok := body["aggregated_edges"].([]interface{})
	if !ok || len(edges) != 1 {
		t.Fatalf("aggregated_edges=%v", body["aggregated_edges"])
	}
	edge := edges[0].(map[string]interface{})
	if edge["routes"] != float64(1) || edge["calls"] != float64(80) {
		t.Fatalf("aggregate=%v", edge)
	}
}

func TestServicePanoramaFiltersNamespaceBeforeAggregation(t *testing.T) {
	h := servicePanoramaHandler(t)
	recorder := httptest.NewRecorder()
	h.ServicePanoramaOverview(recorder, panoramaRequest("/api/v1/services/overview?namespace=shop"))
	body := decodePanoramaResponse(t, recorder)
	if body["total"] != float64(1) || body["calls"] != float64(100) {
		t.Fatalf("filtered overview=%v", body)
	}
}

func TestServiceDependencyMatrixBoundsLargeServiceSets(t *testing.T) {
	h := servicePanoramaHandler(t)
	recorder := httptest.NewRecorder()
	h.ServiceDependencyMatrix(recorder, panoramaRequest("/api/v1/services/dependency-matrix?limit=1"))
	body := decodePanoramaResponse(t, recorder)
	if body["truncated"] != true || body["total_services"] != float64(2) || body["limit"] != float64(1) {
		t.Fatalf("matrix bounds=%v", body)
	}
	if len(body["services"].([]interface{})) != 1 {
		t.Fatalf("matrix services=%v", body["services"])
	}
}

func TestServiceDependenciesReturnsLanesAndCycles(t *testing.T) {
	h := graphTestHandler(t)
	recorder := httptest.NewRecorder()
	req := panoramaRequest("/api/v1/services/service:v1:tenant-a:service/dependencies?upstream_depth=1&downstream_depth=1")
	req.Header.Set("X-Cluster-ID", "cluster-a")
	h.ServiceDependencies(recorder, req)
	body := decodePanoramaResponse(t, recorder)
	if body["center"].(map[string]interface{})["entity_uid"] != "service:v1:tenant-a:service" {
		t.Fatalf("center=%v", body["center"])
	}
	if len(body["downstream"].([]interface{})) != 1 {
		t.Fatalf("downstream=%v", body["downstream"])
	}
	if body["topology_revision"] == "" {
		t.Fatal("dependency response must expose topology_revision")
	}
}
