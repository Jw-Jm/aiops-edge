package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ClusterRouter 分发 /api/v1/clusters 下的操作。
func (h *Handler) ClusterRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/clusters"
	rest := strings.TrimPrefix(r.URL.Path, base+"/")
	if rest == r.URL.Path {
		rest = ""
	}

	// /clusters/sync
	if rest == "sync" && r.Method == http.MethodPost {
		h.clusterSync(w, r)
		return
	}
	// /clusters 集合
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			h.clusterList(w, r)
		case http.MethodPost:
			h.clusterCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	// /clusters/{id} 或 /clusters/{id}/nodes|namespaces|events
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "nodes":
			h.clusterNodes(w, r, id)
			return
		case "namespaces":
			h.clusterNamespaces(w, r, id)
			return
		case "events":
			h.clusterEvents(w, r, id)
			return
		}
	}
	switch r.Method {
	case http.MethodPut:
		h.clusterUpdate(w, r, id)
	case http.MethodDelete:
		h.clusterDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) ClusterList(w http.ResponseWriter, r *http.Request) {
	h.clusterList(w, r)
}

func (h *Handler) clusterList(w http.ResponseWriter, r *http.Request) {
	items, err := (&store.ClusterDAO{}).List()
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"clusters": []store.Cluster{}, "error": err.Error()})
		return
	}
	// scope 过滤：限定集群范围时，只返回授权集群
	sc := currentScope(r)
	if !sc.IsFull() {
		filtered := make([]store.Cluster, 0, len(items))
		for _, c := range items {
			if sc.ContainsCluster(c.Name) {
				filtered = append(filtered, c)
			}
		}
		items = filtered
	}
	// kubeconfig 敏感，列表不返回
	for i := range items {
		items[i].Kubeconfig = ""
	}
	// P1-1: 若集群元数据（node_count/version/api_server）为空，尝试从 kubectl 实时补齐一次，
	// 使默认集群能展示真实节点数/版本/APIServer，无需手动触发 /clusters/sync。
	enriched := false
	for i := range items {
		c := &items[i]
		if c.NodeCount <= 0 || c.APIServer == "" || c.Version == "" {
			info := k8sClusterInfo()
			if info.Name == c.Name || c.Name == "kubernetes-cluster" {
				if info.NodeCount > 0 {
					c.NodeCount = info.NodeCount
				}
				if info.Version != "" {
					c.Version = info.Version
				}
				if info.APIServer != "" {
					c.APIServer = info.APIServer
				}
				if info.Status != "" {
					c.Status = info.Status
				}
				enriched = true
			}
		}
	}
	// 有补齐才写库（幂等 upsert），避免每次列表都触发
	if enriched {
		d := &store.ClusterDAO{}
		for _, c := range items {
			if c.NodeCount > 0 || c.APIServer != "" {
				_, _ = d.Upsert(&store.Cluster{
					Name: c.Name, Provider: c.Provider, Region: c.Region,
					Version: c.Version, NodeCount: c.NodeCount, Status: c.Status, APIServer: c.APIServer,
				})
			}
		}
	}
	respondJSON(w, 200, map[string]interface{}{"clusters": items})
}

// clusterCreate POST /clusters — 新增集群（含 kubeconfig）。
func (h *Handler) clusterCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Name       string `json:"name"`
		Provider   string `json:"provider"`
		APIServer  string `json:"api_server"`
		Kubeconfig string `json:"kubeconfig"`
	}
	if json.Unmarshal(body, &req) != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid JSON"})
		return
	}
	if req.Name == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "name required"})
		return
	}
	if req.Provider == "" {
		req.Provider = "onprem"
	}
	d := &store.ClusterDAO{}
	id, err := d.Create(&store.Cluster{
		Name: req.Name, Provider: req.Provider, APIServer: req.APIServer, Kubeconfig: req.Kubeconfig,
	})
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 201, map[string]interface{}{"ok": true, "id": id})
}

// clusterSync 从 kubectl 自动发现 K8s 集群信息并 upsert。
func (h *Handler) clusterSync(w http.ResponseWriter, r *http.Request) {
	info := k8sClusterInfo()
	if info.Name == "" {
		// kubectl 不可用时降级
		respondJSON(w, 200, map[string]interface{}{"ok": true, "synced": false, "error": "kubectl not available or no cluster"})
		return
	}
	d := &store.ClusterDAO{}
	id, err := d.Upsert(&store.Cluster{
		Name: info.Name, Provider: info.Provider, Version: info.Version,
		NodeCount: info.NodeCount, Status: info.Status, APIServer: info.APIServer,
	})
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true, "synced": true, "id": id, "cluster": info})
}

// clusterNodes 返回集群节点列表（优先用该集群 kubeconfig，否则当前 kubectl context）。
func (h *Handler) clusterNodes(w http.ResponseWriter, r *http.Request, id int64) {
	kc, err := clusterKubeconfig(id)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"nodes": []store.ClusterNode{}, "error": err.Error()})
		return
	}
	if kc == "" {
		nodes := k8sNodes()
		respondJSON(w, 200, map[string]interface{}{"nodes": nodes, "count": len(nodes)})
		return
	}
	nodes := k8sNodesWithKubeconfig(kc)
	respondJSON(w, 200, map[string]interface{}{"nodes": nodes, "count": len(nodes)})
}

// clusterNamespaces 返回集群命名空间列表（kubeconfig 或当前 context）。
func (h *Handler) clusterNamespaces(w http.ResponseWriter, r *http.Request, id int64) {
	kc, err := clusterKubeconfig(id)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"namespaces": []string{}, "error": err.Error()})
		return
	}
	out, err := kubeList(kc, "get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"namespaces": []string{}, "error": err.Error()})
		return
	}
	ns := []string{}
	for _, s := range strings.Fields(strings.TrimSpace(out)) {
		if s != "" {
			ns = append(ns, s)
		}
	}
	respondJSON(w, 200, map[string]interface{}{"namespaces": ns, "count": len(ns)})
}

// clusterEvents 返回集群异常事件列表。
func (h *Handler) clusterEvents(w http.ResponseWriter, r *http.Request, id int64) {
	kc, err := clusterKubeconfig(id)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"events": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	out, err := kubeList(kc, "get", "events", "-A", "-o", "json")
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"events": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	events := parseK8sEvents(out)
	respondJSON(w, 200, map[string]interface{}{"events": events, "count": len(events)})
}

// clusterKubeconfig 返回集群的 kubeconfig（若有）；无则返回空串。
func clusterKubeconfig(id int64) (string, error) {
	d := &store.ClusterDAO{}
	c, err := d.GetByID(id)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", errors.New("cluster not found")
	}
	return c.Kubeconfig, nil
}

// inClusterKubeconfig 从容器内 ServiceAccount 生成 kubeconfig，使 kubectl 可在集群内访问 API。
// 返回空串表示当前不是运行在 K8s 集群内（本地开发/无 SA 时回退到默认 context）。
func inClusterKubeconfig() string {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return ""
	}
	token, err := os.ReadFile(saTokenFile)
	if err != nil {
		return ""
	}
	caData, _ := os.ReadFile(saCACertFile)
	server := "https://" + host
	if port != "" {
		server += ":" + port
	}
	// kubeconfig：cluster + user(SA token) + context
	cfg := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: in-cluster
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: sa
  user:
    token: %s
contexts:
- name: in-cluster
  context:
    cluster: in-cluster
    user: sa
current-context: in-cluster
`, server, base64.StdEncoding.EncodeToString(caData), strings.TrimSpace(string(token)))
	return cfg
}

// kubeList 执行 kubectl；若给定 kubeconfig 则写临时文件后 --kubeconfig 切换；
// 若未给定 kubeconfig，则在集群内自动用 ServiceAccount 生成 in-cluster kubeconfig。
func kubeList(kubeconfig string, args ...string) (string, error) {
	if kubeconfig == "" {
		kubeconfig = inClusterKubeconfig()
	}
	if kubeconfig != "" {
		tmp, err := os.CreateTemp("", "kc-*.yaml")
		if err != nil {
			return "", err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(kubeconfig); err != nil {
			return "", err
		}
		tmp.Close()
		args = append([]string{"--kubeconfig", tmp.Name()}, args...)
	}
	out, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", errors.New(string(ee.Stderr))
		}
		return "", err
	}
	return string(out), nil
}

// parseK8sEvents 解析 kubectl get events -o json 为精简列表。
func parseK8sEvents(raw string) []map[string]interface{} {
	var res struct {
		Items []struct {
			LastTimestamp string `json:"lastTimestamp"`
			Type          string `json:"type"`
			Reason        string `json:"reason"`
			Message       string `json:"message"`
			Involved      struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
		} `json:"items"`
	}
	out := []map[string]interface{}{}
	if json.Unmarshal([]byte(raw), &res) != nil {
		return out
	}
	for _, it := range res.Items {
		if it.Type == "Normal" {
			continue // 只返回异常事件
		}
		out = append(out, map[string]interface{}{
			"last_timestamp": it.LastTimestamp,
			"type":           it.Type,
			"reason":         it.Reason,
			"message":        it.Message,
			"involved_object": it.Involved.Kind + "/" + it.Involved.Name,
		})
	}
	return out
}

// k8sNodesWithKubeconfig 用指定 kubeconfig 获取节点列表。
func k8sNodesWithKubeconfig(kubeconfig string) []store.ClusterNode {
	out, err := kubeList(kubeconfig, "get", "nodes", "-o", "json")
	if err != nil {
		return []store.ClusterNode{}
	}
	return parseK8sNodes([]byte(out))
}

func (h *Handler) clusterUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	body, _ := io.ReadAll(r.Body)
	var req store.Cluster
	json.Unmarshal(body, &req)
	if err := (&store.ClusterDAO{}).Update(id, &req); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) clusterDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := (&store.ClusterDAO{}).Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

type clusterInfo struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Version    string `json:"version"`
	NodeCount  int    `json:"node_count"`
	Status     string `json:"status"`
	APIServer  string `json:"api_server"`
}

// k8sClusterInfo 用 kubectl 读取集群信息。
func k8sClusterInfo() clusterInfo {
	info := clusterInfo{Provider: "kubernetes", Status: "active"}
	// 集群名 + server
	if out, err := exec.Command("kubectl", "config", "view", "-o", "json").Output(); err == nil {
		var cfg struct {
			Clusters []struct {
				Name    string `json:"name"`
				Cluster struct {
					Server string `json:"server"`
				} `json:"cluster"`
			} `json:"clusters"`
		}
		if json.Unmarshal(out, &cfg) == nil && len(cfg.Clusters) > 0 {
			info.Name = cfg.Clusters[0].Name
			info.APIServer = cfg.Clusters[0].Cluster.Server
		}
	}
	if info.Name == "" {
		info.Name = "kubernetes-cluster"
	}
	// 版本 + 节点数
	nodes := k8sNodes()
	info.NodeCount = len(nodes)
	for _, n := range nodes {
		if n.Status != "Ready" {
			info.Status = "degraded"
		}
	}
	// kubectl 1.28+ 移除了 --short，改用 kubectl version 并解析 Server Version 行
	if out, err := exec.Command("kubectl", "version").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Server Version") || strings.HasPrefix(line, "Server Version:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info.Version = strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return info
}

// k8sNodes 用 kubectl 获取节点列表（当前 context）。
func k8sNodes() []store.ClusterNode {
	out, err := exec.Command("kubectl", "get", "nodes", "-o", "json").Output()
	if err != nil {
		return []store.ClusterNode{}
	}
	return parseK8sNodes(out)
}

// parseK8sNodes 解析 kubectl get nodes -o json 为节点列表。
func parseK8sNodes(out []byte) []store.ClusterNode {
	var res struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				CreationTimestamp string            `json:"creationTimestamp"`
				Labels            map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
					OSImage        string `json:"osImage"`
				} `json:"nodeInfo"`
				Capacity struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"capacity"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &res) != nil {
		return []store.ClusterNode{}
	}
	nodes := []store.ClusterNode{}
	for _, it := range res.Items {
		ready := false
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		role := "worker"
		if it.Metadata.Labels["node-role.kubernetes.io/control-plane"] != "" ||
			it.Metadata.Labels["node-role.kubernetes.io/master"] != "" {
			role = "control-plane"
		}
		ip := ""
		for _, a := range it.Status.Addresses {
			if a.Type == "InternalIP" {
				ip = a.Address
			}
		}
		status := "NotReady"
		if ready {
			status = "Ready"
		}
		nodes = append(nodes, store.ClusterNode{
			Name: it.Metadata.Name, Role: role, Status: status, IP: ip,
			OS: it.Status.NodeInfo.OSImage, CPU: it.Status.Capacity.CPU, Memory: it.Status.Capacity.Memory,
			Kubelet: it.Status.NodeInfo.KubeletVersion, CreatedAt: it.Metadata.CreationTimestamp,
		})
	}
	return nodes
}
