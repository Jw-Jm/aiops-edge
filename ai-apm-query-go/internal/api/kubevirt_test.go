package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

const (
	kubeVirtTenantA  = "33333333-3333-4333-8333-333333333333"
	kubeVirtClusterA = "11111111-1111-4111-8111-111111111111"
	kubeVirtClusterB = "22222222-2222-4222-8222-222222222222"
)

// kubeVirtVMIObjects 构造同名不同 UID 的 virtualmachineinstances 列表。
func kubeVirtVMIObjects(uid, phase, node string) []map[string]interface{} {
	return []map[string]interface{}{{
		"metadata": map[string]interface{}{
			"uid": uid, "name": "shared-vm", "namespace": "default", "resourceVersion": "100",
			"creationTimestamp": "2026-09-01T00:00:00Z",
		},
		"status": map[string]interface{}{
			"phase": phase, "nodeName": node, "podName": "virt-launcher-shared-vm",
			"interfaces": []map[string]interface{}{{"ipAddress": "10.0.0." + uid[:1]}},
		},
		"spec": map[string]interface{}{
			"domain": map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"cpu": "2", "memory": "4Gi"},
				},
			},
		},
	}}
}

// kubeVirtSnapshot 返回已安装 KubeVirt 且含一台 VMI 的集群快照。
func kubeVirtSnapshot(uid, phase string) map[string]interface{} {
	return map[string]interface{}{
		"virtual_machines":          []map[string]interface{}{},
		"virtual_machine_instances": kubeVirtVMIObjects(uid, phase, "node-a"),
		"events":                    []map[string]interface{}{},
	}
}

// kubeVirtClusterAccessor 按 canonical cluster_id 返回不同快照，模拟真实多集群边界。
type kubeVirtClusterAccessor struct {
	snapshots    map[string]map[string]interface{}
	errByCluster map[string]error
}

func (a *kubeVirtClusterAccessor) Client(ctx context.Context, clusterID string) (query.KubeClient, error) {
	if err, ok := a.errByCluster[clusterID]; ok {
		return nil, err
	}
	snapshot, ok := a.snapshots[clusterID]
	if !ok {
		return nil, errors.New("identity mismatch: cluster has no validated boundary client")
	}
	return &k8sTestClient{graphObjects: snapshot}, nil
}

// kubeVirtClientWithEvents 支持 ListEvents 的窄事件客户端。
type kubeVirtClientWithEvents struct {
	k8sTestClient
	events    []map[string]interface{}
	eventsErr error
}

func (c *kubeVirtClientWithEvents) ListEvents() ([]map[string]interface{}, error) {
	return c.events, c.eventsErr
}

type kubeVirtEventsAccessor struct{ client query.KubeClient }

func (a *kubeVirtEventsAccessor) Client(ctx context.Context, clusterID string) (query.KubeClient, error) {
	return a.client, nil
}

func kubeVirtHandlerForClusters(snapshots map[string]map[string]interface{}) *Handler {
	return &Handler{kubeRepo: query.NewKubernetesRepository(&kubeVirtClusterAccessor{snapshots: snapshots})}
}

func kubeVirtRequest(method, target string, auth *AuthorizationContext) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if auth != nil {
		req = withAuthorizationContext(req, *auth)
	}
	return req
}

func TestVMsUsesAuthorizedCanonicalCluster(t *testing.T) {
	h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
		kubeVirtClusterA: kubeVirtSnapshot("uid-a", "Running"),
		kubeVirtClusterB: kubeVirtSnapshot("uid-b", "Stopped"),
	})
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterA}
	rec := httptest.NewRecorder()
	h.VMs(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms", &auth))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "uid-b") || !strings.Contains(body, "uid-a") {
		t.Fatalf("cross-cluster response: %s", body)
	}
}

func TestVMDetailUsesAuthorizedCanonicalCluster(t *testing.T) {
	h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
		kubeVirtClusterA: kubeVirtSnapshot("uid-a", "Running"),
		kubeVirtClusterB: kubeVirtSnapshot("uid-b", "Stopped"),
	})
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterB}
	rec := httptest.NewRecorder()
	h.VMDetail(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms/default/shared-vm", &auth))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "uid-b") {
		t.Fatalf("detail must read active cluster only: %s", rec.Body.String())
	}
}

func TestVMsRequiresAuthorizationContext(t *testing.T) {
	h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
		kubeVirtClusterA: kubeVirtSnapshot("uid-a", "Running"),
	})
	rec := httptest.NewRecorder()
	h.VMs(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing authorization must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVMDetailRequiresAuthorizationContext(t *testing.T) {
	h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
		kubeVirtClusterA: kubeVirtSnapshot("uid-a", "Running"),
	})
	rec := httptest.NewRecorder()
	h.VMDetail(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms/default/shared-vm", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing authorization must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVMsMapsPermissionDeniedFromBoundary(t *testing.T) {
	h := &Handler{kubeRepo: query.NewKubernetesRepository(&kubeVirtClusterAccessor{
		errByCluster: map[string]error{kubeVirtClusterA: errors.New("identity mismatch")},
	})}
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterA}
	rec := httptest.NewRecorder()
	h.VMs(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms", &auth))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("identity mismatch must be 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVMsMapsUnavailableFromBoundary(t *testing.T) {
	h := &Handler{kubeRepo: query.NewKubernetesRepository(&kubeVirtClusterAccessor{
		errByCluster: map[string]error{kubeVirtClusterA: errors.New("dial tcp: connection refused")},
	})}
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterA}
	rec := httptest.NewRecorder()
	h.VMs(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms", &auth))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("backend failure must be 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVMsRejectsNonCanonicalCluster(t *testing.T) {
	h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
		kubeVirtClusterA: kubeVirtSnapshot("uid-a", "Running"),
	})
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: "default"}
	rec := httptest.NewRecorder()
	h.VMs(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms", &auth))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-canonical cluster must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVMsDistinguishesNotInstalledFromInstalledButEmpty(t *testing.T) {
	notInstalled := &Handler{kubeRepo: query.NewKubernetesRepository(&kubeVirtClusterAccessor{
		snapshots: map[string]map[string]interface{}{
			// CRD 不存在时 kubeGraphObjects 不会写入这两个键。
			kubeVirtClusterA: {},
		},
	})}
	installedEmpty := &Handler{kubeRepo: query.NewKubernetesRepository(&kubeVirtClusterAccessor{
		snapshots: map[string]map[string]interface{}{
			kubeVirtClusterA: {
				"virtual_machines":          []map[string]interface{}{},
				"virtual_machine_instances": []map[string]interface{}{},
			},
		},
	})}
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterA}

	for name, tc := range map[string]struct {
		h         *Handler
		installed bool
	}{
		"not installed":   {notInstalled, false},
		"installed empty": {installedEmpty, true},
	} {
		rec := httptest.NewRecorder()
		tc.h.VMs(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms", &auth))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", name, rec.Code, rec.Body.String())
		}
		var body struct {
			Installed bool `json:"installed"`
			Count     int  `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s unmarshal: %v", name, err)
		}
		if body.Installed != tc.installed || body.Count != 0 {
			t.Fatalf("%s installed=%v count=%d want installed=%v", name, body.Installed, body.Count, tc.installed)
		}
	}
}

func TestVMDetailNotFoundUsesExactNamespaceAndName(t *testing.T) {
	h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
		kubeVirtClusterA: kubeVirtSnapshot("uid-a", "Running"),
	})
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterA}
	rec := httptest.NewRecorder()
	h.VMDetail(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms/other-namespace/shared-vm", &auth))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-namespace lookup must be 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVMsSurfacesEventsAndPartial(t *testing.T) {
	client := &kubeVirtClientWithEvents{
		k8sTestClient: k8sTestClient{
			graphObjects: map[string]interface{}{
				"virtual_machine_instances": kubeVirtVMIObjects("uid-a", "Running", "node-a"),
			},
		},
		events: []map[string]interface{}{{"reason": "FailedScheduling", "involved_object": "VirtualMachineInstance/shared-vm"}},
	}
	h := &Handler{kubeRepo: query.NewKubernetesRepository(&kubeVirtEventsAccessor{client: client})}
	auth := AuthorizationContext{UserID: "operator", TenantID: kubeVirtTenantA, ActiveClusterID: kubeVirtClusterA}
	rec := httptest.NewRecorder()
	h.VMDetail(rec, kubeVirtRequest(http.MethodGet, "/api/v1/infrastructure/vms/default/shared-vm", &auth))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "FailedScheduling") {
		t.Fatalf("events must surface: %s", rec.Body.String())
	}
}
