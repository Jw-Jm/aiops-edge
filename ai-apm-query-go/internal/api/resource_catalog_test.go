package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
)

func resourceCatalogTestHandler(t *testing.T) *Handler {
	t.Helper()
	repo := graphpkg.NewMemoryRepository()
	vertices := []graphpkg.Entity{
		{EntityUID: "asset:server-01", EntityType: "physical_server", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "server-01", NameKey: "server-01", Source: "inventory", Status: "active", Health: "degraded", LastSeenMS: 100, Attrs: map[string]interface{}{"vendor": "Dell", "model": "R750", "serial_number": "SN-01", "provider_url": "https://internal.invalid"}},
		{EntityUID: "k8s:node-01", EntityType: "k8s_node", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "node-01", NameKey: "node-01", Source: "k8s", Status: "active", Health: "healthy", LastSeenMS: 100},
		{EntityUID: "k8s:pod-01", EntityType: "pod", TenantID: "tenant-a", ClusterID: "cluster-a", Namespace: "payments", Name: "api-01", NameKey: "api-01", Source: "k8s", Status: "active", Health: "critical", LastSeenMS: 100},
		{EntityUID: "event:cpu-01", EntityType: "cpu", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "cpu-01", NameKey: "cpu-01", Source: "inventory", Status: "active", Health: "healthy", LastSeenMS: 100},
		{EntityUID: "asset:server-other-cluster", EntityType: "physical_server", TenantID: "tenant-a", ClusterID: "cluster-b", Name: "server-b", NameKey: "server-b", Source: "inventory", Status: "active", Health: "healthy", LastSeenMS: 100},
		{EntityUID: "asset:other-tenant", EntityType: "physical_server", TenantID: "tenant-b", ClusterID: "cluster-a", Name: "secret", NameKey: "secret", Source: "inventory", Status: "active", Health: "healthy", LastSeenMS: 100},
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Source: "test", Vertices: vertices[:4]}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-b", Source: "test", Vertices: vertices[4:5]}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-b", ClusterID: "cluster-a", Source: "test", Vertices: vertices[5:]}); err != nil {
		t.Fatal(err)
	}
	return &Handler{graphRepo: repo}
}

func resourceRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return withAuthorizationContext(req, AuthorizationContext{UserID: "user", SessionID: "session", TenantID: "tenant-a", ActiveClusterID: "cluster-a"})
}

func TestResourceCatalogFiltersByAuthorizedClusterAndDomain(t *testing.T) {
	rec := httptest.NewRecorder()
	resourceCatalogTestHandler(t).ResourceCatalog(rec, resourceRequest(http.MethodGet, "/api/v1/resources/catalog?domain=compute&q=server&limit=10"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			UID       string `json:"uid"`
			ClusterID string `json:"cluster_id"`
			Type      string `json:"type"`
			Domain    string `json:"domain"`
		} `json:"items"`
		Meta ResourceReadMeta `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].UID != "asset:server-01" || body.Items[0].ClusterID != "cluster-a" || body.Items[0].Type != "physical_server" || body.Items[0].Domain != "compute" {
		t.Fatalf("items=%+v", body.Items)
	}
	if body.Meta.Partial || body.Meta.Stale || len(body.Meta.WarningCodes) != 0 {
		t.Fatalf("meta=%+v", body.Meta)
	}
}

func TestResourceCatalogExcludesEvidenceAndEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	resourceCatalogTestHandler(t).ResourceCatalog(rec, resourceRequest(http.MethodGet, "/api/v1/resources/catalog?limit=100"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Items {
		if item.Type == "cpu" || item.Type == "alert" || item.Type == "change" || item.Type == "case" || item.Type == "migration" {
			t.Fatalf("evidence/event leaked into catalog: %+v", item)
		}
	}
	if len(body.Items) != 3 {
		t.Fatalf("items=%+v, want three selectable resources in cluster-a", body.Items)
	}
}

func TestResourceSummaryAlwaysReturnsFiveDomains(t *testing.T) {
	rec := httptest.NewRecorder()
	resourceCatalogTestHandler(t).ResourceSummary(rec, resourceRequest(http.MethodGet, "/api/v1/resources/summary"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Domains []struct {
			Domain string `json:"domain"`
			Count  int    `json:"count"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Domains) != 5 {
		t.Fatalf("domains=%+v", body.Domains)
	}
	if body.Domains[0].Domain != "compute" || body.Domains[0].Count != 2 {
		t.Fatalf("compute summary=%+v", body.Domains[0])
	}
}

func TestResourceDetailRejectsCrossClusterAndReportsNotFound(t *testing.T) {
	h := resourceCatalogTestHandler(t)
	crossCluster := httptest.NewRecorder()
	h.ResourceDetail(crossCluster, resourceRequest(http.MethodGet, "/api/v1/resources/detail?uid=asset%3Aserver-other-cluster"))
	if crossCluster.Code != http.StatusForbidden {
		t.Fatalf("cross cluster status=%d body=%s", crossCluster.Code, crossCluster.Body.String())
	}
	notFound := httptest.NewRecorder()
	h.ResourceDetail(notFound, resourceRequest(http.MethodGet, "/api/v1/resources/detail?uid=asset%3Amissing"))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d body=%s", notFound.Code, notFound.Body.String())
	}
}

func TestResourceDetailAllowListsOperationalAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	resourceCatalogTestHandler(t).ResourceDetail(rec, resourceRequest(http.MethodGet, "/api/v1/resources/detail?uid=asset%3Aserver-01"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Attributes map[string]interface{} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Attributes["vendor"] != "Dell" || body.Data.Attributes["model"] != "R750" {
		t.Fatalf("attributes=%+v", body.Data.Attributes)
	}
	if _, leaked := body.Data.Attributes["provider_url"]; leaked {
		t.Fatalf("sensitive attribute leaked: %+v", body.Data.Attributes)
	}
}

func TestResourceSummaryFailsClosedWhenGraphUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).ResourceSummary(rec, resourceRequest(http.MethodGet, "/api/v1/resources/summary"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "" || !containsResourceError(rec.Body.String(), "RESOURCE_CATALOG_UNAVAILABLE") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func containsResourceError(body, expected string) bool {
	return len(body) >= len(expected) && stringContains(body, expected)
}

func stringContains(body, expected string) bool {
	for i := 0; i+len(expected) <= len(body); i++ {
		if body[i:i+len(expected)] == expected {
			return true
		}
	}
	return false
}
