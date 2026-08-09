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
	"strings"
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
		pods = append(pods, map[string]interface{}{"name": it.Metadata.Name, "namespace": it.Metadata.Namespace, "status": it.Status.Phase, "restarts": rc})
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
