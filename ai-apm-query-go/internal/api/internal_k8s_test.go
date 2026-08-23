package api

import (
	"context"
	"testing"

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

type k8sTestAccessor struct {
	client query.KubeClient
}

func (a *k8sTestAccessor) Client(ctx context.Context, clusterID string) (query.KubeClient, error) {
	return a.client, nil
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
