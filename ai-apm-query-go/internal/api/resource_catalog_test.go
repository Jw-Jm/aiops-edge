package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

type resourceFeatureUnavailableRepository struct{ *graphpkg.MemoryRepository }

func (r resourceFeatureUnavailableRepository) SearchEntities(context.Context, graphpkg.GraphScope, graphpkg.EntitySearchQuery) ([]graphpkg.Entity, error) {
	return nil, graphpkg.NewError(graphpkg.ErrGraphFeatureUnavailable, "HugeGraph entity search requires graph_entity_alias")
}

func TestResourceCatalogFallsBackToBoundedKubernetesSnapshotWhenGraphSearchIsUnavailable(t *testing.T) {
	const clusterID = "11111111-1111-4111-8111-111111111111"
	snapshot := map[string]interface{}{
		"deployments": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-deploy", "name": "api", "namespace": "payments"},
			"status":   map[string]interface{}{"availableReplicas": float64(1), "replicas": float64(1)},
		}},
		"pods": []map[string]interface{}{{
			"metadata": map[string]interface{}{"uid": "uid-pod", "name": "api-1", "namespace": "payments"},
			"status":   map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
	}
	h := &Handler{
		graphRepo: resourceFeatureUnavailableRepository{MemoryRepository: graphpkg.NewMemoryRepository()},
		kubeRepo:  query.NewKubernetesRepository(&k8sTestAccessor{client: &k8sTestClient{graphObjects: snapshot}}),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/catalog?group=containers&limit=20", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", SessionID: "session", TenantID: "tenant-a", ActiveClusterID: clusterID})
	rec := httptest.NewRecorder()
	h.ResourceCatalog(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			UID       string `json:"uid"`
			ClusterID string `json:"cluster_id"`
			Type      string `json:"type"`
			Health    string `json:"health"`
		} `json:"items"`
		Meta ResourceReadMeta `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].ClusterID != clusterID || body.Meta.Partial {
		t.Fatalf("items=%+v meta=%+v", body.Items, body.Meta)
	}
	seen := map[string]string{}
	for _, item := range body.Items {
		seen[item.Type] = item.UID + ":" + item.Health
	}
	if seen["deployment"] == "" || seen["pod"] == "" {
		t.Fatalf("fallback projection=%v", seen)
	}
}

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

func TestResourceCatalogFiltersByTypeNamespaceAndFreshness(t *testing.T) {
	rec := httptest.NewRecorder()
	resourceCatalogTestHandler(t).ResourceCatalog(rec, resourceRequest(http.MethodGet, "/api/v1/resources/catalog?group=containers&type=pod&namespace=payments&freshness=stale&limit=10"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			UID       string `json:"uid"`
			Type      string `json:"type"`
			Namespace string `json:"namespace"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].UID != "k8s:pod-01" || body.Items[0].Type != "pod" || body.Items[0].Namespace != "payments" {
		t.Fatalf("items=%+v", body.Items)
	}
}

func TestResourceCatalogContainerGroupExposesOnlyPrimaryKinds(t *testing.T) {
	repo := graphpkg.NewMemoryRepository()
	entities := []graphpkg.Entity{
		{EntityUID: "deployment:one", EntityType: "deployment", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one", Source: "k8s", Status: "active"},
		{EntityUID: "statefulset:one", EntityType: "statefulset", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-statefulset", Source: "k8s", Status: "active"},
		{EntityUID: "daemonset:one", EntityType: "daemonset", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-daemonset", Source: "k8s", Status: "active"},
		{EntityUID: "job:one", EntityType: "job", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-job", Source: "k8s", Status: "active"},
		{EntityUID: "cronjob:one", EntityType: "cronjob", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-cronjob", Source: "k8s", Status: "active"},
		{EntityUID: "pod:one", EntityType: "pod", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-pod", Source: "k8s", Status: "active"},
		{EntityUID: "service:one", EntityType: "k8s_service", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-service", Source: "k8s", Status: "active"},
		{EntityUID: "ingress:one", EntityType: "ingress", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-ingress", Source: "k8s", Status: "active"},
		{EntityUID: "container:one", EntityType: "container", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-container", Source: "k8s", Status: "active"},
		{EntityUID: "replicaset:one", EntityType: "replicaset", TenantID: "tenant-a", ClusterID: "cluster-a", Name: "one", NameKey: "one-replicaset", Source: "k8s", Status: "active"},
	}
	if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Source: "test", Vertices: entities}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{graphRepo: repo}
	rec := httptest.NewRecorder()
	h.ResourceCatalog(rec, resourceRequest(http.MethodGet, "/api/v1/resources/catalog?group=containers&limit=20"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range body.Items {
		seen[item.Type] = true
	}
	for _, expected := range []string{"deployment", "statefulset", "daemonset", "job", "cronjob", "pod", "k8s_service", "ingress"} {
		if !seen[expected] {
			t.Fatalf("missing primary kind %q in %#v", expected, seen)
		}
	}
	if seen["container"] || seen["replicaset"] || body.Total != 8 {
		t.Fatalf("group projection leaked aggregate kinds or total=%d: %#v", body.Total, seen)
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

func TestResourceDetailAllowsTypedKubeVirtDependencies(t *testing.T) {
	dependencies := map[string]interface{}{"disks": []interface{}{map[string]interface{}{"device_name": "rootdisk", "volume_name": "rootdisk"}}}
	attrs := resourceDetailAttributes(graphpkg.Entity{EntityType: "vmi", Attrs: map[string]interface{}{"vm_dependencies": dependencies, "raw_payload": "must-not-leak"}})
	if _, ok := attrs["vm_dependencies"]; !ok {
		t.Fatalf("typed dependencies missing from detail attributes: %+v", attrs)
	}
	if _, ok := attrs["raw_payload"]; ok {
		t.Fatalf("raw payload leaked into detail attributes: %+v", attrs)
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
