package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// k8sTestClient implements query.KubeClient for repository-level test.
type k8sTestClient struct {
	nodes       []string
	pods        []query.KubePod
	nodeDetails []map[string]interface{}
}

func (c *k8sTestClient) ClusterID() string                          { return "" }
func (c *k8sTestClient) ListNodeNames() ([]string, error)           { return c.nodes, nil }
func (c *k8sTestClient) ListNodeDetails() ([]map[string]interface{}, error) { return c.nodeDetails, nil }
func (c *k8sTestClient) ListPods(ns string) ([]query.KubePod, error) { return c.pods, nil }
func (c *k8sTestClient) GetDeploymentIdentity(namespace, name string) (query.KubeObjectIdentity, error) {
	return query.KubeObjectIdentity{UID: "uid-1", ResourceVersion: "42", Namespace: namespace, Name: name}, nil
}

type k8sTestAccessor struct {
	client query.KubeClient
}

func (a *k8sTestAccessor) Client(ctx context.Context, clusterID string) (query.KubeClient, error) {
	return a.client, nil
}

// k8sFailClient 模拟 K8s 子查询部分失败（ListPods 失败，nodes/details 成功）。
type k8sFailClient struct {
	k8sTestClient
}

func (c *k8sFailClient) ListPods(ns string) ([]query.KubePod, error) {
	return nil, errors.New("k8s pods backend down")
}

// k8sAllFailClient 模拟全部子查询失败。
type k8sAllFailClient struct{}

func (c *k8sAllFailClient) ClusterID() string { return "" }
func (c *k8sAllFailClient) ListNodeNames() ([]string, error) {
	return nil, errors.New("nodes down")
}
func (c *k8sAllFailClient) ListNodeDetails() ([]map[string]interface{}, error) {
	return nil, errors.New("details down")
}
func (c *k8sAllFailClient) ListPods(ns string) ([]query.KubePod, error) {
	return nil, errors.New("pods down")
}
func (c *k8sAllFailClient) GetDeploymentIdentity(namespace, name string) (query.KubeObjectIdentity, error) {
	return query.KubeObjectIdentity{}, errors.New("identity down")
}

// TestKubeRepoReturnsNodesPodsAndDetails 验证 KubernetesRepository 通过边界 accessor
// 返回节点名、节点详情与 Pod 列表（P19 内部边界 K8s 数据路径的 repo 层）。
func TestKubeRepoReturnsNodesPodsAndDetails(t *testing.T) {
	acc := &k8sTestAccessor{client: &k8sTestClient{
		nodes:       []string{"node-a"},
		pods:        []query.KubePod{{Name: "pod-1", Namespace: "obs", Status: "Running", Restarts: 3}},
		nodeDetails: []map[string]interface{}{{"name": "node-a", "status": "Ready"}},
	}}
	repo := query.NewKubernetesRepository(acc)
	scope := query.KubernetesScope{TenantID: "t", ClusterID: "11111111-1111-4111-8111-111111111111"}

	nodes, err := repo.ListNodeNames(context.Background(), scope, scope.ClusterID)
	if err != nil || len(nodes) != 1 || nodes[0] != "node-a" {
		t.Fatalf("ListNodeNames: %v %v", nodes, err)
	}
	pods, err := repo.ListPods(context.Background(), scope, scope.ClusterID, "all")
	if err != nil || len(pods) != 1 || pods[0].Name != "pod-1" || pods[0].Restarts != 3 {
		t.Fatalf("ListPods: %v %v", pods, err)
	}
	details, err := repo.ListNodeDetails(context.Background(), scope, scope.ClusterID)
	if err != nil || len(details) != 1 || details[0]["name"] != "node-a" {
		t.Fatalf("ListNodeDetails: %v %v", details, err)
	}
}

// newK8sQueryHandler 构造带 kubeRepo 的 internal query handler，复用 newInternalQueryTestHandler
// 的 internal verifier/sqlmock 设置，仅替换 kubeRepo。
func newK8sQueryHandler(t *testing.T, client query.KubeClient) *internalQueryTestCtx {
	t.Helper()
	c := newInternalQueryTestHandler(t, nil)
	c.h.kubeRepo = query.NewKubernetesRepository(&k8sTestAccessor{client: client})
	return c
}

// TestInternalQueryKubernetesPartial 验证 K8s 子查询部分失败时返回 partial + errors，
// 不得吞成 200 空数组（F-19 / A0-05）。
func TestInternalQueryKubernetesPartial(t *testing.T) {
	c := newK8sQueryHandler(t, &k8sFailClient{k8sTestClient{
		nodes:       []string{"node-a"},
		nodeDetails: []map[string]interface{}{{"name": "node-a", "status": "Ready"}},
	}})
	rec := httptest.NewRecorder()
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/kubernetes", `{}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "kubernetes.resources.read"
	})
	c.h.InternalQueryKubernetes(rec, req)
	if rec.Code != 200 {
		t.Fatalf("partial should still be 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"partial":true`) {
		t.Fatalf("expected partial:true, got %s", body)
	}
	if !strings.Contains(body, "pods backend down") {
		t.Fatalf("expected pods error surfaced, got %s", body)
	}
}

// TestInternalQueryKubernetesAllFail 验证全部子查询失败时返回 503（不伪装成空集合）。
func TestInternalQueryKubernetesAllFail(t *testing.T) {
	c := newK8sQueryHandler(t, &k8sAllFailClient{})
	rec := httptest.NewRecorder()
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/kubernetes", `{}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "kubernetes.resources.read"
	})
	c.h.InternalQueryKubernetes(rec, req)
	if rec.Code != 503 {
		t.Fatalf("all-fail should be 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
