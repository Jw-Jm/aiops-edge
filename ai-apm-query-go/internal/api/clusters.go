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

	"github.com/observability-platform/ai-apm-query-go/internal/k8sboundary"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// clusterRegistrationRequest is the browser-safe registration contract. Raw
// kubeconfig material is intentionally not represented here; credentials are
// resolved only from a management-plane Secret by k8sboundary.
type clusterRegistrationRequest struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Environment   string `json:"environment"`
	Region        string `json:"region"`
	CredentialRef string `json:"credential_ref"`
	Type          string `json:"type"`
	Capabilities  string `json:"capabilities"`
	Labels        string `json:"labels"`
}

func parseClusterRegistrationRequest(r io.Reader, tenantID string) (k8sboundary.ClusterRegistration, error) {
	if strings.TrimSpace(tenantID) == "" {
		return k8sboundary.ClusterRegistration{}, errors.New("tenant context required")
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return k8sboundary.ClusterRegistration{}, fmt.Errorf("read registration request: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return k8sboundary.ClusterRegistration{}, errors.New("invalid JSON")
	}
	if _, ok := fields["kubeconfig"]; ok {
		return k8sboundary.ClusterRegistration{}, errors.New("raw kubeconfig is not accepted; use credential_ref")
	}
	var req clusterRegistrationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return k8sboundary.ClusterRegistration{}, errors.New("invalid JSON")
	}
	if strings.TrimSpace(req.CredentialRef) == "" {
		return k8sboundary.ClusterRegistration{}, errors.New("credential_ref is required")
	}
	if strings.TrimSpace(req.Slug) == "" || strings.TrimSpace(req.Name) == "" {
		return k8sboundary.ClusterRegistration{}, errors.New("slug and name are required")
	}
	return k8sboundary.ClusterRegistration{
		TenantID: tenantID, Slug: strings.TrimSpace(req.Slug), Name: strings.TrimSpace(req.Name),
		Environment: strings.TrimSpace(req.Environment), Region: strings.TrimSpace(req.Region),
		CredentialRef: strings.TrimSpace(req.CredentialRef), Type: strings.TrimSpace(req.Type),
		Capabilities: strings.TrimSpace(req.Capabilities), Labels: strings.TrimSpace(req.Labels),
	}, nil
}

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
	// /clusters/{cluster_id} 或 /clusters/{cluster_id}/nodes|namespaces|events
	parts := strings.Split(rest, "/")
	if len(parts) == 2 {
		switch parts[1] {
		case "nodes":
			h.clusterNodes(w, r, parts[0])
			return
		case "namespaces":
			h.clusterNamespaces(w, r, parts[0])
			return
		case "events":
			h.clusterEvents(w, r, parts[0])
			return
		}
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "bad cluster reference", 400)
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
	tenantID := extractTenantID(r)
	var items []store.Cluster
	var err error
	if tenantID != "" {
		items, err = (&store.ClusterDAO{}).ListForTenant(tenantID)
	} else {
		items, err = (&store.ClusterDAO{}).List()
	}
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
	registration, err := parseClusterRegistrationRequest(r.Body, extractTenantID(r))
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	if h.clusterRegistrar == nil {
		respondJSON(w, 503, map[string]interface{}{"error": "cluster registration boundary unavailable"})
		return
	}
	cluster, err := h.clusterRegistrar.Register(registration)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrClusterIdentityDuplicate) {
			status = http.StatusConflict
		}
		respondJSON(w, status, map[string]interface{}{"error": err.Error()})
		return
	}
	auditWrite(r, "cluster.create", cluster.ClusterID, "注册集群 slug="+registration.Slug)
	respondJSON(w, http.StatusCreated, map[string]interface{}{"ok": true, "cluster_id": cluster.ClusterID, "cluster": cluster})
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

func clusterForRef(ref string) (*store.Cluster, error) {
	ref = strings.TrimSpace(ref)
	if canonicalUUID.MatchString(ref) {
		return (&store.ClusterDAO{}).GetByClusterID(ref)
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return nil, store.ErrInvalidClusterRef
	}
	return (&store.ClusterDAO{}).GetByIDCanonical(id)
}

func (h *Handler) validatedClusterClient(ref string) (*k8sboundary.Client, error) {
	c, err := clusterForRef(ref)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, store.ErrClusterNotFound
	}
	if h.clusterClients == nil {
		return nil, errors.New("cluster access boundary unavailable")
	}
	return h.clusterClients.GetClient(c.ClusterID)
}

// clusterNodes uses the identity-validated Kubernetes boundary. The legacy
// clusters.kubeconfig column is never read by this request path.
func (h *Handler) clusterNodes(w http.ResponseWriter, r *http.Request, ref string) {
	c, err := clusterForRef(ref)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"nodes": []store.ClusterNode{}, "error": err.Error()})
		return
	}
	if c == nil {
		respondJSON(w, 404, map[string]interface{}{"nodes": []store.ClusterNode{}, "error": "cluster not found"})
		return
	}
	client, err := h.validatedClusterClient(c.ClusterID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"nodes": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	nodes, err := client.KubeNodeDetails()
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"nodes": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"nodes": nodes, "count": len(nodes), "cluster_id": c.ClusterID})
}

// clusterNamespaces uses the identity-validated Kubernetes boundary.
func (h *Handler) clusterNamespaces(w http.ResponseWriter, r *http.Request, ref string) {
	c, err := clusterForRef(ref)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"namespaces": []string{}, "error": err.Error()})
		return
	}
	client, err := h.validatedClusterClient(c.ClusterID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"namespaces": []string{}, "error": err.Error()})
		return
	}
	ns, err := client.KubeNamespaces()
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"namespaces": []string{}, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"namespaces": ns, "count": len(ns), "cluster_id": c.ClusterID})
}

// clusterEvents 返回集群异常事件列表。
func (h *Handler) clusterEvents(w http.ResponseWriter, r *http.Request, ref string) {
	c, err := clusterForRef(ref)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"events": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	client, err := h.validatedClusterClient(c.ClusterID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"events": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	events, err := client.KubeEvents()
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"events": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"events": events, "count": len(events), "cluster_id": c.ClusterID})
}

// errNoKubeconfig 非默认集群未配置 kubeconfig 时的守卫错误。
// 安全(P1-1)：阻止 namespaces/events 静默回退 in-cluster / 当前 context，
// 避免泄漏真实集群数据。
var errNoKubeconfig = errors.New("cluster has no kubeconfig")

// clusterKubeconfig 返回集群的 kubeconfig（若有）；无则返回空串。
// 守卫下沉（P1-1）：非默认集群（id!=1 且非 kubernetes-cluster）无 kubeconfig 时
// 返回 errNoKubeconfig，与 clusterNodes 守卫同款，不静默回退。
func clusterKubeconfig(id int64) (string, error) {
	d := &store.ClusterDAO{}
	c, err := d.GetByID(id)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", errors.New("cluster not found")
	}
	// P3.8: no id=1 / kubernetes-cluster / current-context fallback (V9.2 §9).
	// Any cluster without an explicit kubeconfig fails closed.
	if c.Kubeconfig == "" {
		return "", errNoKubeconfig
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
// 兼容 events.k8s.io 格式（P2-4 修复）：时间字段按 lastTimestamp→eventTime→firstTimestamp
// 依次取（events.k8s.io 只有 eventTime）；involvedObject 缺失时用 regarding 字段；
// 增加 count 字段。Warning/Error 事件不被丢弃。
func parseK8sEvents(raw string) []map[string]interface{} {
	var res struct {
		Items []struct {
			LastTimestamp  string `json:"lastTimestamp"`
			EventTime      string `json:"eventTime"`
			FirstTimestamp string `json:"firstTimestamp"`
			Type           string `json:"type"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			Count          int32  `json:"count"`
			Involved       struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
			Regarding struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"regarding"`
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
		// events.k8s.io 无 lastTimestamp/firstTimestamp，用 eventTime；依次回退
		ts := it.LastTimestamp
		if ts == "" {
			ts = it.EventTime
		}
		if ts == "" {
			ts = it.FirstTimestamp
		}
		objKind, objName := it.Involved.Kind, it.Involved.Name
		if objKind == "" && objName == "" {
			objKind, objName = it.Regarding.Kind, it.Regarding.Name
		}
		out = append(out, map[string]interface{}{
			"last_timestamp":  ts,
			"type":            it.Type,
			"reason":          it.Reason,
			"message":         it.Message,
			"count":           it.Count,
			"involved_object": objKind + "/" + objName,
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
	auditWrite(r, "cluster.update", strconv.FormatInt(id, 10), "更新集群 "+req.Name)
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) clusterDelete(w http.ResponseWriter, r *http.Request, id int64) {
	d := &store.ClusterDAO{}
	// P3-1 修复：删除不存在的集群返回 404。
	existing, err := d.GetByID(id)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	if existing == nil {
		respondJSON(w, 404, map[string]interface{}{"error": "cluster not found"})
		return
	}
	if err := d.Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	auditWrite(r, "cluster.delete", existing.Name, "删除集群")
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

type clusterInfo struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Version   string `json:"version"`
	NodeCount int    `json:"node_count"`
	Status    string `json:"status"`
	APIServer string `json:"api_server"`
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
