package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	saTokenFile  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCACertFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	apiHost      = "https://kubernetes.default.svc"
)

var k8sClient *http.Client

func init() {
	k8sClient = &http.Client{}
	// Try loading CA cert
	if caData, err := os.ReadFile(saCACertFile); err == nil {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caData)
		k8sClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}
	}
}

func (h *Handler) Nodes(w http.ResponseWriter, r *http.Request) {
	data, err := k8sAPI("/api/v1/nodes")
	if err != nil {
		// K8s 不可达：返回空节点 + 错误说明（不伪造节点，避免误导）
		respondJSON(w, 200, map[string]interface{}{
			"nodes": []map[string]interface{}{},
			"mock":  false,
			"error": err.Error(),
		})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"nodes": parseNodes(data)})
}

func (h *Handler) Pods(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	path := "/api/v1/pods"
	if ns != "" && ns != "all" { path = fmt.Sprintf("/api/v1/namespaces/%s/pods", ns) }
	data, err := k8sAPI(path)
	if err != nil {
		// K8s 不可达：返回空列表 + 错误说明（不伪造 Pod，避免误导展示不存在的资源）
		respondJSON(w, 200, map[string]interface{}{
			"pods":  []map[string]interface{}{},
			"mock":  false,
			"error": err.Error(),
		})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"pods": parsePods(data)})
}

func (h *Handler) Deployments(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	path := "/apis/apps/v1/deployments"
	if ns != "" && ns != "all" { path = fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments", ns) }
	data, err := k8sAPI(path)
	if err != nil {
		// K8s 不可达：返回空列表 + 错误说明（不伪造 Deployment）
		respondJSON(w, 200, map[string]interface{}{
			"deployments": []map[string]interface{}{},
			"mock":        false,
			"error":       err.Error(),
		})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"deployments": parseDeployments(data)})
}

func (h *Handler) Namespaces(w http.ResponseWriter, r *http.Request) {
	data, err := k8sAPI("/api/v1/namespaces")
	if err != nil {
		// K8s 不可达：返回空列表 + 错误说明（不伪造 namespace）
		respondJSON(w, 200, map[string]interface{}{
			"namespaces": []map[string]interface{}{},
			"mock":       false,
			"error":      err.Error(),
		})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"namespaces": parseNamespaces(data)})
}

func k8sAPI(path string) ([]byte, error) {
	token, err := os.ReadFile(saTokenFile)
	if err != nil { return kubectlFallback(path) }

	req, _ := http.NewRequest("GET", apiHost+path, nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))

	resp, err := k8sClient.Do(req)
	if err != nil { return kubectlFallback(path) }
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func kubectlFallback(path string) ([]byte, error) {
	args := []string{"get"}
	if strings.Contains(path, "/metrics.k8s.io") {
		return exec.Command("kubectl", "top", "nodes", "-o", "json").Output()
	}
	switch {
	case strings.Contains(path, "/nodes"):
		args = append(args, "nodes", "-o", "json")
	case strings.Contains(path, "/namespaces") && strings.Contains(path, "/pods"):
		parts := strings.Split(path, "/")
		for i, p := range parts {
			if p == "namespaces" && i+1 < len(parts) { args = append(args, "pods", "-n", parts[i+1], "-o", "json"); break }
		}
	case strings.Contains(path, "/namespaces") && strings.Contains(path, "/deployments"):
		parts := strings.Split(path, "/")
		for i, p := range parts {
			if p == "namespaces" && i+1 < len(parts) { args = append(args, "deployments", "-n", parts[i+1], "-o", "json"); break }
		}
	case strings.Contains(path, "/pods"):
		args = append(args, "pods", "--all-namespaces", "-o", "json")
	case strings.Contains(path, "/deployments"):
		args = append(args, "deployments", "--all-namespaces", "-o", "json")
	case strings.Contains(path, "/namespaces"):
		args = append(args, "namespaces", "-o", "json")
	}
	return exec.Command("kubectl", args...).Output()
}

func parseNodes(data []byte) []map[string]interface{} {
	var r struct {
		Items []struct {
			Metadata struct{ Name string }
			Status   struct {
				Conditions []struct{ Type, Status string }
				NodeInfo   struct{ KubeletVersion string }
				Capacity   map[string]string
				Allocatable map[string]string
			}
		}
	}
	json.Unmarshal(data, &r)
	nodes := []map[string]interface{}{}
	for _, it := range r.Items {
		ready := "NotReady"
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" { ready = "Ready" }
		}
		cpu := it.Status.Capacity["cpu"]
		if cpu == "" { cpu = it.Status.Allocatable["cpu"] }
		mem := it.Status.Capacity["memory"]
		if mem == "" { mem = it.Status.Allocatable["memory"] }
		nodes = append(nodes, map[string]interface{}{"name": it.Metadata.Name, "status": ready, "cpu": cpu, "memory": mem, "version": it.Status.NodeInfo.KubeletVersion})
	}
	return nodes
}

func parsePods(data []byte) []map[string]interface{} {
	var r struct {
		Items []struct {
			Metadata struct{ Name, Namespace string }
			Spec     struct{ NodeName string `json:"nodeName"` }
			Status   struct {
				Phase             string
				ContainerStatuses []struct{ RestartCount int } `json:"containerStatuses"`
			}
		}
	}
	json.Unmarshal(data, &r)
	pods := []map[string]interface{}{}
	for _, it := range r.Items {
		rc := 0
		if len(it.Status.ContainerStatuses) > 0 { rc = it.Status.ContainerStatuses[0].RestartCount }
		pods = append(pods, map[string]interface{}{"name": it.Metadata.Name, "namespace": it.Metadata.Namespace, "status": it.Status.Phase, "restarts": rc, "node_name": it.Spec.NodeName})
	}
	return pods
}

func parseDeployments(data []byte) []map[string]interface{} {
	var r struct {
		Items []struct {
			Metadata struct{ Name, Namespace string }
			Status   struct{ Replicas, ReadyReplicas int }
		}
	}
	json.Unmarshal(data, &r)
	deps := []map[string]interface{}{}
	for _, it := range r.Items {
		deps = append(deps, map[string]interface{}{"name": it.Metadata.Name, "namespace": it.Metadata.Namespace, "replicas": it.Status.Replicas, "ready": it.Status.ReadyReplicas})
	}
	return deps
}

func parseNamespaces(data []byte) []map[string]interface{} {
	var r struct {
		Items []struct {
			Metadata struct{ Name string }
			Status   struct{ Phase string }
		}
	}
	json.Unmarshal(data, &r)
	nss := []map[string]interface{}{}
	for _, it := range r.Items {
		nss = append(nss, map[string]interface{}{"name": it.Metadata.Name, "status": it.Status.Phase})
	}
	return nss
}

// parseQuantity 解析 K8s Quantity 为浮点值（CPU 核心数 / 内存 MiB 基数的 KB 数）。
// 支持 CPU: "250m"(0.25核), "2"(2核)；内存: "1Gi"=1024, "500Mi"=500, "12345Ki"=12345。
// 返回的数值统一到"原单位"的数值：CPU 返回核心数，内存返回以 Ki 为基数（1Gi=1024Ki）。
// 返回的数值统一到"原单位"的数值：CPU 返回核心数，内存返回以 Ki 为基数（1Gi=1024Ki）。
func parseQuantity(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" { return 0 }
	// 后缀映射：CPU 的 m=0.001；内存的 Ki/Mi/Gi 以 Ki 为基数
	mult := 1.0
	numStr := s
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "n"):
		// metrics-server 的 CPU usage 以 nanocores 返回（如 "1113552929n"），1n=1e-9 核
		mult = 1e-9
		numStr = s[:len(s)-1]
	case strings.HasSuffix(lower, "m"):
		mult = 0.001
		numStr = s[:len(s)-1]
	case strings.HasSuffix(lower, "ki"):
		mult = 1.0; numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "mi"):
		mult = 1024.0; numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "gi"):
		mult = 1024.0 * 1024.0; numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "k"):
		mult = 1000.0; numStr = s[:len(s)-1]
	case strings.HasSuffix(lower, "g"):
		mult = 1e9; numStr = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil { return 0 }
	return v * mult
}

// parseNodeMetrics 解析 MetricsList + capacity，返回每节点实时用量（usage_pct）。
func parseNodeMetrics(data []byte, capacities map[string]map[string]string) []map[string]interface{} {
	var r struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Usage    map[string]string     `json:"usage"`
		} `json:"items"`
	}
	json.Unmarshal(data, &r)
	out := []map[string]interface{}{}
	for _, it := range r.Items {
		cpuUsage := parseQuantity(it.Usage["cpu"])       // 核心数
		memUsage := parseQuantity(it.Usage["memory"])    // Ki 基数
		cpuCap := parseQuantity(capacities[it.Metadata.Name]["cpu"])
		memCap := parseQuantity(capacities[it.Metadata.Name]["memory"])
		cpuPct := 0.0
		if cpuCap > 0 { cpuPct = cpuUsage / cpuCap * 100 }
		memPct := 0.0
		if memCap > 0 { memPct = memUsage / memCap * 100 }
		out = append(out, map[string]interface{}{
			"node": it.Metadata.Name,
			"cpu_usage": it.Usage["cpu"], "cpu_capacity": capacities[it.Metadata.Name]["cpu"],
			"cpu_usage_pct": round2(cpuPct),
			"mem_usage": it.Usage["memory"], "mem_capacity": capacities[it.Metadata.Name]["memory"],
			"mem_usage_pct": round2(memPct),
		})
	}
	return out
}

// k8sAPIFn 可注入（测试用）；默认指向 k8sAPI。
var k8sAPIFn = k8sAPI

// NodesMetrics 处理 GET /api/v1/nodes/metrics — 节点实时 CPU/内存用量（metrics-server）。
func (h *Handler) NodesMetrics(w http.ResponseWriter, r *http.Request) {
	data, err := k8sAPIFn("/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"nodes": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	// capacity 从 in-cluster /api/v1/nodes 读取（不依赖 kubectl）
	capMap := map[string]map[string]string{}
	if nd, nerr := k8sAPIFn("/api/v1/nodes"); nerr == nil {
		for _, n := range parseNodes(nd) {
			name, _ := n["name"].(string)
			cpu, _ := n["cpu"].(string)
			mem, _ := n["memory"].(string)
			if name != "" {
				capMap[name] = map[string]string{"cpu": cpu, "memory": mem}
			}
		}
	}
	respondJSON(w, 200, map[string]interface{}{"nodes": parseNodeMetrics(data, capMap)})
}

// PodDetail 处理 GET /api/v1/infrastructure/pods/{namespace}/{name}
// 返回容器列表（名称/镜像/状态/restartCount/ready）、Pod 状态、节点、IP、
// 创建时间、资源请求/限制（CPU/mem）及该 Pod 最近事件（复用 parseK8sEvents 逻辑）。
func (h *Handler) PodDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/infrastructure/pods/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "expected /api/v1/infrastructure/pods/{namespace}/{name}"})
		return
	}
	ns, name := parts[0], parts[1]

	data, err := k8sAPI("/api/v1/namespaces/" + ns + "/pods/" + name)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"pod": nil, "error": err.Error()})
		return
	}
	var pod struct {
		Metadata struct {
			Name              string `json:"name"`
			Namespace         string `json:"namespace"`
			CreationTimestamp string `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			NodeName string `json:"nodeName"`
			Containers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
				Resources struct {
					Requests map[string]string `json:"requests"`
					Limits   map[string]string `json:"limits"`
				} `json:"resources"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			Phase             string `json:"phase"`
			HostIP            string `json:"hostIP"`
			PodIP             string `json:"podIP"`
			ContainerStatuses []struct {
				Name         string                 `json:"name"`
				Image        string                 `json:"image"`
				Ready        bool                   `json:"ready"`
				RestartCount int                    `json:"restartCount"`
				State        map[string]interface{} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	}
	if json.Unmarshal(data, &pod) != nil {
		respondJSON(w, 200, map[string]interface{}{"pod": nil, "error": "parse pod failed"})
		return
	}

	containers := []map[string]interface{}{}
	for _, c := range pod.Status.ContainerStatuses {
		state := "unknown"
		for k := range c.State {
			state = k // running / terminated / waiting
		}
		containers = append(containers, map[string]interface{}{
			"name": c.Name, "image": c.Image, "state": state,
			"ready": c.Ready, "restart_count": c.RestartCount,
		})
	}
	resources := []map[string]interface{}{}
	for _, c := range pod.Spec.Containers {
		resources = append(resources, map[string]interface{}{
			"name":     c.Name,
			"requests": map[string]string{"cpu": c.Resources.Requests["cpu"], "memory": c.Resources.Requests["memory"]},
			"limits":   map[string]string{"cpu": c.Resources.Limits["cpu"], "memory": c.Resources.Limits["memory"]},
		})
	}

	respondJSON(w, 200, map[string]interface{}{
		"name":       pod.Metadata.Name,
		"namespace":  pod.Metadata.Namespace,
		"status":     pod.Status.Phase,
		"node":       pod.Spec.NodeName,
		"ip":         pod.Status.PodIP,
		"host_ip":    pod.Status.HostIP,
		"created_at": pod.Metadata.CreationTimestamp,
		"containers": containers,
		"resources":  resources,
		"events":     objectEvents(ns, name),
	})
}

// HPA 处理 GET /api/v1/infrastructure/hpa — 集群 HPA 列表（跨命名空间）。
// 复用 system.go getHPAStatus 的解析思路，返回结构化列表。
func (h *Handler) HPA(w http.ResponseWriter, r *http.Request) {
	done := make(chan map[string]interface{}, 1)
	go func() {
		out, err := kubeList("", "get", "hpa", "-A", "-o", "json")
		if err != nil {
			done <- map[string]interface{}{"hpa": []map[string]interface{}{}, "error": err.Error()}
			return
		}
		var hpaList struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					MinReplicas *int32 `json:"minReplicas"`
					MaxReplicas int32  `json:"maxReplicas"`
				} `json:"spec"`
				Status struct {
					CurrentReplicas int32 `json:"currentReplicas"`
					DesiredReplicas int32 `json:"desiredReplicas"`
					CurrentMetrics  []struct {
						Type     string `json:"type"`
						Resource struct {
							Name    string `json:"name"`
							Current struct {
								AverageUtilization int32 `json:"averageUtilization"`
							} `json:"current"`
						} `json:"resource"`
					} `json:"currentMetrics"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &hpaList) != nil {
			done <- map[string]interface{}{"hpa": []map[string]interface{}{}, "error": "parse hpa failed"}
			return
		}
		items := []map[string]interface{}{}
		for _, it := range hpaList.Items {
			cpuUtil := int32(0)
			for _, m := range it.Status.CurrentMetrics {
				if m.Type == "Resource" && m.Resource.Name == "cpu" {
					cpuUtil = m.Resource.Current.AverageUtilization
				}
			}
			min := int32(1)
			if it.Spec.MinReplicas != nil {
				min = *it.Spec.MinReplicas
			}
			items = append(items, map[string]interface{}{
				"name":             it.Metadata.Name,
				"namespace":        it.Metadata.Namespace,
				"current_replicas": it.Status.CurrentReplicas,
				"desired_replicas": it.Status.DesiredReplicas,
				"min":              min,
				"max":              it.Spec.MaxReplicas,
				"cpu_utilization":  cpuUtil,
			})
		}
		done <- map[string]interface{}{"hpa": items, "count": len(items)}
	}()
	select {
	case res := <-done:
		respondJSON(w, 200, res)
	case <-time.After(5 * time.Second):
		respondJSON(w, 200, map[string]interface{}{"hpa": []map[string]interface{}{}, "error": "kubectl timeout"})
	}
}

// objectEvents 查询指定命名空间中某个对象的异常事件（复用 parseK8sEvents 逻辑）。
// 用 kubectl --field-selector 过滤，3s 超时，失败返回空列表。
func objectEvents(ns, name string) []map[string]interface{} {
	done := make(chan []map[string]interface{}, 1)
	go func() {
		out, err := kubeList("", "get", "events", "-n", ns,
			"--field-selector", fmt.Sprintf("involvedObject.name=%s", name), "-o", "json")
		if err != nil {
			done <- []map[string]interface{}{}
			return
		}
		done <- parseK8sEvents(out)
	}()
	select {
	case ev := <-done:
		return ev
	case <-time.After(3 * time.Second):
		return []map[string]interface{}{}
	}
}
