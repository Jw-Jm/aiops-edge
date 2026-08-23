package query

import (
	"context"
	"errors"
	"testing"
)

// fakeKubeAccessor 模拟已冻结的 K8s Access Boundary（k8sboundary.ClusterClientManager）。
type fakeKubeAccessor struct {
	nodes []string
	pods  []KubePod
	err   error
}

func (f *fakeKubeAccessor) Client(ctx context.Context, clusterID string) (KubeClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &fakeKubeClient{nodes: f.nodes, pods: f.pods}, nil
}

type fakeKubeClient struct {
	nodes []string
	pods  []KubePod
}

func (c *fakeKubeClient) ClusterID() string            { return "" }
func (c *fakeKubeClient) ListNodeNames() ([]string, error) { return c.nodes, nil }
func (c *fakeKubeClient) ListNodeDetails() ([]map[string]interface{}, error) { return nil, nil }
func (c *fakeKubeClient) ListPods(ns string) ([]KubePod, error) { return c.pods, nil }

func TestKubeRepoListNodeNames(t *testing.T) {
	acc := &fakeKubeAccessor{nodes: []string{"node-a", "node-b"}}
	r := NewKubernetesRepository(acc)
	nodes, err := r.ListNodeNames(context.Background(), KubernetesScope{TenantID: "t1"}, "3f3c3b3a-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatalf("ListNodeNames: %v", err)
	}
	if len(nodes) != 2 || nodes[0] != "node-a" || nodes[1] != "node-b" {
		t.Fatalf("nodes = %v", nodes)
	}
}

func TestKubeRepoAccessDeniedMapsPermissionDenied(t *testing.T) {
	acc := &fakeKubeAccessor{err: errors.New("identity mismatch")}
	r := NewKubernetesRepository(acc)
	_, err := r.ListNodeNames(context.Background(), KubernetesScope{TenantID: "t1"}, "3f3c3b3a-0000-4000-8000-000000000001")
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != PermissionDeniedCode {
		t.Fatalf("expected permission_denied, got %v", err)
	}
}

func TestKubeRepoEmptyIsNoData(t *testing.T) {
	acc := &fakeKubeAccessor{nodes: nil}
	r := NewKubernetesRepository(acc)
	_, err := r.ListNodeNames(context.Background(), KubernetesScope{TenantID: "t1"}, "3f3c3b3a-0000-4000-8000-000000000001")
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected no_data, got %v", err)
	}
}

func TestKubeRepoInvalidClusterRef(t *testing.T) {
	acc := &fakeKubeAccessor{}
	r := NewKubernetesRepository(acc)
	_, err := r.ListNodeNames(context.Background(), KubernetesScope{TenantID: "t1"}, "not-a-uuid")
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != PermissionDeniedCode {
		t.Fatalf("expected permission_denied for invalid cluster ref, got %v", err)
	}
}
