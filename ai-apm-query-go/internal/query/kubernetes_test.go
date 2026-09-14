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

// fakeKubeVirtClient 模拟带窄事件能力的边界客户端。
type fakeKubeVirtClient struct {
	objects   map[string]interface{}
	events    []map[string]interface{}
	eventsErr error
}

func (c *fakeKubeVirtClient) ClusterID() string                                  { return "" }
func (c *fakeKubeVirtClient) ListNodeNames() ([]string, error)                   { return nil, nil }
func (c *fakeKubeVirtClient) ListNodeDetails() ([]map[string]interface{}, error) { return nil, nil }
func (c *fakeKubeVirtClient) ListPods(ns string) ([]KubePod, error)              { return nil, nil }
func (c *fakeKubeVirtClient) GetDeploymentIdentity(namespace, name string) (KubeObjectIdentity, error) {
	return KubeObjectIdentity{}, nil
}
func (c *fakeKubeVirtClient) ListGraphObjects() (map[string]interface{}, error) {
	return c.objects, nil
}
func (c *fakeKubeVirtClient) ListEvents() ([]map[string]interface{}, error) {
	return c.events, c.eventsErr
}

const kubeVirtTestCluster = "3f3c3b3a-0000-4000-8000-000000000001"

// TestKubeRepoListKubeVirtObjectsReportsInstalledFromKeyPresence 验证 CRD 不存在与
// 已安装但为空必须可区分：installed 来自键存在性，而非列表长度。
func TestKubeRepoListKubeVirtObjectsReportsInstalledFromKeyPresence(t *testing.T) {
	notInstalled := NewKubernetesRepository(&fakeKubeAccessorWithClient{client: &fakeKubeVirtClient{objects: map[string]interface{}{}}})
	got, err := notInstalled.ListKubeVirtObjects(context.Background(), KubernetesScope{}, kubeVirtTestCluster)
	if err != nil {
		t.Fatalf("ListKubeVirtObjects: %v", err)
	}
	if got["installed"] != false {
		t.Fatalf("missing CRD must report installed=false, got %v", got["installed"])
	}

	installedEmpty := NewKubernetesRepository(&fakeKubeAccessorWithClient{client: &fakeKubeVirtClient{objects: map[string]interface{}{
		"virtual_machines":          []map[string]interface{}{},
		"virtual_machine_instances": []map[string]interface{}{},
	}}})
	got, err = installedEmpty.ListKubeVirtObjects(context.Background(), KubernetesScope{}, kubeVirtTestCluster)
	if err != nil {
		t.Fatalf("ListKubeVirtObjects: %v", err)
	}
	if got["installed"] != true {
		t.Fatalf("empty CRD list must report installed=true, got %v", got["installed"])
	}
}

// TestKubeRepoListKubeVirtObjectsKeepsVMsWhenEventsFail 验证事件读取失败时
// 保留 VM/VMI 数据并标记 partial，不伪造完整结果。
func TestKubeRepoListKubeVirtObjectsKeepsVMsWhenEventsFail(t *testing.T) {
	repo := NewKubernetesRepository(&fakeKubeAccessorWithClient{client: &fakeKubeVirtClient{
		objects: map[string]interface{}{
			"virtual_machine_instances": []map[string]interface{}{{"metadata": map[string]interface{}{"name": "vm-1"}}},
		},
		eventsErr: errors.New("events backend down"),
	}})
	got, err := repo.ListKubeVirtObjects(context.Background(), KubernetesScope{}, kubeVirtTestCluster)
	if err != nil {
		t.Fatalf("ListKubeVirtObjects: %v", err)
	}
	if got["partial"] != true {
		t.Fatalf("event failure must mark partial, got %v", got["partial"])
	}
	if _, ok := got["events"]; ok {
		t.Fatalf("failed event read must not fabricate an events payload")
	}
	codes, _ := got["warning_codes"].([]string)
	found := false
	for _, code := range codes {
		if code == KubeVirtWarningEventsUnavailable {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s warning code, got %v", KubeVirtWarningEventsUnavailable, got["warning_codes"])
	}
}

// TestKubeRepoListKubeVirtObjectsSurfacesEvents 验证事件读取成功时进入固定输出。
func TestKubeRepoListKubeVirtObjectsSurfacesEvents(t *testing.T) {
	repo := NewKubernetesRepository(&fakeKubeAccessorWithClient{client: &fakeKubeVirtClient{
		objects: map[string]interface{}{
			"virtual_machine_instances": []map[string]interface{}{{"metadata": map[string]interface{}{"name": "vm-1"}}},
		},
		events: []map[string]interface{}{{"reason": "FailedScheduling"}},
	}})
	got, err := repo.ListKubeVirtObjects(context.Background(), KubernetesScope{}, kubeVirtTestCluster)
	if err != nil {
		t.Fatalf("ListKubeVirtObjects: %v", err)
	}
	if got["partial"] != false {
		t.Fatalf("successful read must not be partial, got %v", got["partial"])
	}
	events, ok := got["events"].([]map[string]interface{})
	if !ok || len(events) != 1 || events[0]["reason"] != "FailedScheduling" {
		t.Fatalf("events = %#v", got["events"])
	}
}

// TestKubeRepoListKubeVirtObjectsFailsClosedWithoutGraphCapability 验证未配置图能力时
// fail closed（unavailable），而不是返回伪造空集。
func TestKubeRepoListKubeVirtObjectsFailsClosedWithoutGraphCapability(t *testing.T) {
	repo := NewKubernetesRepository(&fakeKubeAccessorWithClient{client: &fakeKubeClient{}})
	_, err := repo.ListKubeVirtObjects(context.Background(), KubernetesScope{}, kubeVirtTestCluster)
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable, got %v", err)
	}
}
