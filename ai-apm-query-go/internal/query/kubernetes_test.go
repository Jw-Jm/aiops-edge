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
	nodes      []string
	pods       []KubePod
	identities map[string]KubeObjectIdentity
}

func (c *fakeKubeClient) ClusterID() string                                  { return "" }
func (c *fakeKubeClient) ListNodeNames() ([]string, error)                   { return c.nodes, nil }
func (c *fakeKubeClient) ListNodeDetails() ([]map[string]interface{}, error) { return nil, nil }
func (c *fakeKubeClient) ListPods(ns string) ([]KubePod, error)              { return c.pods, nil }
func (c *fakeKubeClient) GetDeploymentIdentity(namespace, name string) (KubeObjectIdentity, error) {
	return KubeObjectIdentity{UID: "uid-1", ResourceVersion: "42", Namespace: namespace, Name: name}, nil
}

func (c *fakeKubeClient) GetObjectIdentity(resourceType, namespace, name string) (KubeObjectIdentity, error) {
	if identity, ok := c.identities[resourceType+":"+namespace+":"+name]; ok {
		return identity, nil
	}
	return KubeObjectIdentity{UID: "uid-1", ResourceVersion: "42", Namespace: namespace, Name: name}, nil
}

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

func TestKubeRepoResolvesCanonicalObjectIdentity(t *testing.T) {
	client := &fakeKubeClient{identities: map[string]KubeObjectIdentity{
		"pod:prod:orders-abc": {UID: "pod-uid", ResourceVersion: "9", Namespace: "prod", Name: "orders-abc"},
		"node::node-a":        {UID: "node-uid", ResourceVersion: "11", Name: "node-a"},
	}}
	r := NewKubernetesRepository(&fakeKubeAccessorWithClient{client: client})
	ctx := context.Background()
	for _, tt := range []struct {
		resourceType, namespace, name, uid string
	}{
		{"pod", "prod", "orders-abc", "pod-uid"},
		{"node", "", "node-a", "node-uid"},
	} {
		got, err := r.GetObjectIdentity(ctx, KubernetesScope{}, "3f3c3b3a-0000-4000-8000-000000000001", tt.resourceType, tt.namespace, tt.name)
		if err != nil || got.UID != tt.uid {
			t.Fatalf("GetObjectIdentity(%s) = %#v, %v", tt.resourceType, got, err)
		}
	}
}

type fakeKubeAccessorWithClient struct{ client KubeClient }

func (f *fakeKubeAccessorWithClient) Client(ctx context.Context, clusterID string) (KubeClient, error) {
	return f.client, nil
}
