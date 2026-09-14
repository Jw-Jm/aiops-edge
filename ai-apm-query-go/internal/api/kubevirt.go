package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// kubeVirtReadScope 从登录态构造 canonical 集群 Scope。
// 与资源目录一致：必须存在授权上下文且 ActiveClusterID 非空，否则 fail closed。
func (h *Handler) kubeVirtReadScope(r *http.Request) (query.KubernetesScope, error) {
	auth, ok := requestAuthorizationContext(r)
	if !ok {
		return query.KubernetesScope{}, authorizationFailure("permission_denied")
	}
	clusterID := strings.TrimSpace(auth.ActiveClusterID)
	if clusterID == "" || auth.TenantID == "" {
		return query.KubernetesScope{}, authorizationFailure("permission_denied")
	}
	return query.KubernetesScope{TenantID: auth.TenantID, ClusterID: clusterID}, nil
}

// respondKubeVirtError 将边界错误映射为统一 HTTP 语义：
// permission_denied → 403、backend_unavailable → 503、no_data → 404/200 由调用方决定。
func respondKubeVirtError(w http.ResponseWriter, err error, notFoundCode string) {
	var authErr *authorizationError
	if errors.As(err, &authErr) {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": authErr.Error()})
		return
	}
	var queryErr *query.QueryError
	if errors.As(err, &queryErr) {
		status := queryErr.HTTPStatus()
		if status == http.StatusOK {
			status = http.StatusServiceUnavailable
		}
		respondJSON(w, status, map[string]interface{}{"error": queryErr.Code, "message": queryErr.Message})
		return
	}
	if notFoundCode != "" && errors.Is(err, errKubeVirtObjectNotFound) {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": notFoundCode})
		return
	}
	respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "KUBEVIRT_UNAVAILABLE"})
}

// errKubeVirtObjectNotFound 表示目标 Namespace/Name 在当前集群中不存在。
var errKubeVirtObjectNotFound = errors.New("kubevirt object not found")

// VMs 处理 GET /api/v1/infrastructure/vms — KubeVirt 虚拟机实例列表。
//
// 只读取登录态活动集群，经 canonical cluster_id → credential_ref → Secret →
// kubeconfig → kube-system UID 边界获取数据；不使用进程默认 kubeconfig 或
// current context，也不按名称跨集群回退。
func (h *Handler) VMs(w http.ResponseWriter, r *http.Request) {
	scope, err := h.kubeVirtReadScope(r)
	if err != nil {
		respondKubeVirtError(w, err, "")
		return
	}
	if h.kubeRepo == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "KUBEVIRT_UNAVAILABLE"})
		return
	}
	snapshot, err := h.kubeRepo.ListKubeVirtObjects(r.Context(), scope, scope.ClusterID)
	if err != nil {
		respondKubeVirtError(w, err, "")
		return
	}
	vms := projectKubeVirtInstances(snapshot["virtual_machine_instances"])
	installed, _ := snapshot["installed"].(bool)
	partial, _ := snapshot["partial"].(bool)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"vms":                    vms,
		"count":                  len(vms),
		"installed":              installed,
		"kubevirt_not_installed": !installed,
		"partial":                partial,
		"warning_codes":          snapshot["warning_codes"],
		"events":                 kubeVirtEvents(snapshot),
	})
}

// VMDetail 处理 GET /api/v1/infrastructure/vms/{namespace}/{name} — 单台 VMI 详情。
// 严格按 namespace + name 精确匹配当前授权集群的实例；不跨 Namespace 或不跨集群回退。
func (h *Handler) VMDetail(w http.ResponseWriter, r *http.Request) {
	scope, err := h.kubeVirtReadScope(r)
	if err != nil {
		respondKubeVirtError(w, err, "")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/infrastructure/vms/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "expected /api/v1/infrastructure/vms/{namespace}/{name}"})
		return
	}
	namespace, name := parts[0], parts[1]

	if h.kubeRepo == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "KUBEVIRT_UNAVAILABLE"})
		return
	}
	snapshot, err := h.kubeRepo.ListKubeVirtObjects(r.Context(), scope, scope.ClusterID)
	if err != nil {
		respondKubeVirtError(w, err, "")
		return
	}
	instances := projectKubeVirtInstances(snapshot["virtual_machine_instances"])
	var (
		target  map[string]interface{}
		foundNS bool
	)
	for _, instance := range instances {
		if boundaryToString(instance["namespace"]) != namespace {
			continue
		}
		if boundaryToString(instance["name"]) != name {
			continue
		}
		target = instance
		break
	}
	if target == nil {
		// 区分“该 Namespace 下没有同名对象”与“该集群完全没有 KubeVirt 对象”。
		for _, instance := range instances {
			if boundaryToString(instance["namespace"]) == namespace {
				foundNS = true
				break
			}
		}
		if !foundNS && len(instances) == 0 {
			installed, _ := snapshot["installed"].(bool)
			if !installed {
				respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": "KUBEVIRT_NOT_INSTALLED"})
				return
			}
		}
		respondKubeVirtError(w, errKubeVirtObjectNotFound, "VM_NOT_FOUND")
		return
	}

	partial, _ := snapshot["partial"].(bool)
	detail := map[string]interface{}{}
	for key, value := range target {
		detail[key] = value
	}
	detail["events"] = filterKubeVirtEvents(kubeVirtEvents(snapshot), name)
	detail["partial"] = partial
	detail["warning_codes"] = snapshot["warning_codes"]
	respondJSON(w, http.StatusOK, detail)
}

// projectKubeVirtInstances 把边界返回的 VMI 列表投影为稳定的 typed 视图。
// 保留 metadata.uid / resourceVersion，使多集群同名对象可区分。
func projectKubeVirtInstances(raw interface{}) []map[string]interface{} {
	items := kubeVirtObjectList(raw)
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		metadata, _ := item["metadata"].(map[string]interface{})
		status, _ := item["status"].(map[string]interface{})
		spec, _ := item["spec"].(map[string]interface{})
		domain, _ := spec["domain"].(map[string]interface{})
		resources, _ := domain["resources"].(map[string]interface{})
		requests, _ := resources["requests"].(map[string]interface{})
		limits, _ := resources["limits"].(map[string]interface{})

		ip := ""
		if interfaces, ok := status["interfaces"].([]interface{}); ok {
			for _, rawIface := range interfaces {
				iface, _ := rawIface.(map[string]interface{})
				if candidate := boundaryToString(iface["ipAddress"]); candidate != "" {
					ip = candidate
					break
				}
			}
		} else if interfaces, ok := status["interfaces"].([]map[string]interface{}); ok {
			for _, iface := range interfaces {
				if candidate := boundaryToString(iface["ipAddress"]); candidate != "" {
					ip = candidate
					break
				}
			}
		}

		cpu := boundaryToString(requests["cpu"])
		if cpu == "" {
			cpu = boundaryToString(limits["cpu"])
		}
		memory := boundaryToString(requests["memory"])
		if memory == "" {
			memory = boundaryToString(limits["memory"])
		}

		out = append(out, map[string]interface{}{
			"name":             boundaryToString(metadata["name"]),
			"namespace":        boundaryToString(metadata["namespace"]),
			"uid":              boundaryToString(metadata["uid"]),
			"resource_version": boundaryToString(metadata["resourceVersion"]),
			"status":           boundaryToString(status["phase"]),
			"node":             boundaryToString(status["nodeName"]),
			"pod":              boundaryToString(status["podName"]),
			"ip":               ip,
			"cpu":              cpu,
			"memory":           memory,
			"created_at":       boundaryToString(metadata["creationTimestamp"]),
		})
	}
	return out
}

// kubeVirtObjectList 兼容 []map[string]interface{} 与 []interface{} 两种解码形状。
// kubectlJSON 解码为 []map[string]interface{}；测试快照可能直接构造为 []interface{}。
func kubeVirtObjectList(raw interface{}) []map[string]interface{} {
	switch typed := raw.(type) {
	case []map[string]interface{}:
		return typed
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]interface{}); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

// kubeVirtEvents 提取边界事件投影。
func kubeVirtEvents(snapshot map[string]interface{}) []map[string]interface{} {
	return kubeVirtObjectList(snapshot["events"])
}

// filterKubeVirtEvents 只保留与目标对象相关的 Warning 事件。
func filterKubeVirtEvents(events []map[string]interface{}, name string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		involved := boundaryToString(event["involved_object"])
		if involved == "" || strings.HasSuffix(involved, "/"+name) {
			out = append(out, event)
		}
	}
	return out
}
