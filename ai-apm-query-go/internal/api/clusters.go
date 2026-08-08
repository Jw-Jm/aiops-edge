package api

import (
	"encoding/json"
	"io"
	"net/http"
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
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	// /clusters/{id} 或 /clusters/{id}/nodes
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if len(parts) == 2 && parts[1] == "nodes" {
		h.clusterNodes(w, r, id)
		return
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
	respondJSON(w, 200, map[string]interface{}{"clusters": items})
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

// clusterNodes 返回集群节点列表（从 kubectl 读取）。
func (h *Handler) clusterNodes(w http.ResponseWriter, r *http.Request, id int64) {
	nodes := k8sNodes()
	respondJSON(w, 200, map[string]interface{}{"nodes": nodes, "count": len(nodes)})
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
	if out, err := exec.Command("kubectl", "version", "--short").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Server Version") || strings.HasPrefix(line, "Server") {
				info.Version = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			}
		}
	}
	return info
}

// k8sNodes 用 kubectl 获取节点列表。
func k8sNodes() []store.ClusterNode {
	out, err := exec.Command("kubectl", "get", "nodes", "-o", "json").Output()
	if err != nil {
		return []store.ClusterNode{}
	}
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
		if it.Metadata.Labels["node-role.kubernetes.io/control-plane"] == "" &&
			it.Metadata.Labels["node-role.kubernetes.io/master"] == "" {
			// 无 control-plane 标签则为 worker
		} else {
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
