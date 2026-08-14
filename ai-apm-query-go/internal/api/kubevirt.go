package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// VMs 处理 GET /api/v1/infrastructure/vms — KubeVirt 虚拟机实例列表。
// 用 kubectl get vmi -A -o json 查询；CRD 未安装/不可用时返回 200 + 空列表 +
// kubevirt_not_installed 标记（不 500，前端可优雅降级）。
func (h *Handler) VMs(w http.ResponseWriter, r *http.Request) {
	done := make(chan map[string]interface{}, 1)
	go func() {
		out, err := kubeList("", "get", "vmi", "-A", "-o", "json")
		if err != nil {
			done <- map[string]interface{}{
				"vms":                    []map[string]interface{}{},
				"count":                  0,
				"kubevirt_not_installed": isKubevirtNotFound(err.Error()),
				"error":                  err.Error(),
			}
			return
		}
		vms, perr := parseVMIs(out)
		if perr != nil {
			done <- map[string]interface{}{"vms": []map[string]interface{}{}, "count": 0, "error": perr.Error()}
			return
		}
		done <- map[string]interface{}{"vms": vms, "count": len(vms), "kubevirt_not_installed": false}
	}()
	select {
	case res := <-done:
		respondJSON(w, 200, res)
	case <-time.After(8 * time.Second):
		respondJSON(w, 200, map[string]interface{}{"vms": []map[string]interface{}{}, "count": 0, "error": "kubectl timeout"})
	}
}

// VMDetail 处理 GET /api/v1/infrastructure/vms/{namespace}/{name} — 单台 VM 详情。
// 返回状态/节点/IP/guest agent 在线/事件。CRD 缺失同样降级返回。
func (h *Handler) VMDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/infrastructure/vms/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "expected /api/v1/infrastructure/vms/{namespace}/{name}"})
		return
	}
	ns, name := parts[0], parts[1]

	done := make(chan map[string]interface{}, 1)
	go func() {
		out, err := kubeList("", "get", "vmi", "-n", ns, name, "-o", "json")
		if err != nil {
			done <- map[string]interface{}{
				"error":                  err.Error(),
				"kubevirt_not_installed": isKubevirtNotFound(err.Error()),
			}
			return
		}
		var res struct {
			Metadata struct {
				Name              string `json:"name"`
				Namespace         string `json:"namespace"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				Phase      string `json:"phase"`
				Node       string `json:"nodeName"`
				PodName    string `json:"podName"`
				Interfaces []struct {
					Name          string `json:"name"`
					IPAddress     string `json:"ipAddress"`
					MAC           string `json:"mac"`
					InterfaceName string `json:"interfaceName"`
				} `json:"interfaces"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
			Spec struct {
				Domain struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
						Limits   map[string]string `json:"limits"`
					} `json:"resources"`
				} `json:"domain"`
			} `json:"spec"`
		}
		if json.Unmarshal([]byte(out), &res) != nil {
			done <- map[string]interface{}{"error": "parse vmi failed"}
			return
		}
		guestAgent := false
		for _, c := range res.Status.Conditions {
			if c.Type == "AgentConnected" && c.Status == "True" {
				guestAgent = true
			}
		}
		ips := []string{}
		for _, iface := range res.Status.Interfaces {
			if iface.IPAddress != "" {
				ips = append(ips, iface.IPAddress)
			}
		}
		cpu := res.Spec.Domain.Resources.Requests["cpu"]
		mem := res.Spec.Domain.Resources.Requests["memory"]
		if cpu == "" {
			cpu = res.Spec.Domain.Resources.Limits["cpu"]
		}
		if mem == "" {
			mem = res.Spec.Domain.Resources.Limits["memory"]
		}
		done <- map[string]interface{}{
			"name":        res.Metadata.Name,
			"namespace":   res.Metadata.Namespace,
			"status":      res.Status.Phase,
			"node":        res.Status.Node,
			"pod":         res.Status.PodName,
			"ip":          ips,
			"cpu":         cpu,
			"memory":      mem,
			"guest_agent": guestAgent,
			"created_at":  res.Metadata.CreationTimestamp,
			"events":      objectEvents(ns, name),
		}
	}()
	select {
	case res := <-done:
		respondJSON(w, 200, res)
	case <-time.After(8 * time.Second):
		respondJSON(w, 200, map[string]interface{}{"error": "kubectl timeout"})
	}
}

// isKubevirtNotFound 判断 kubectl 错误是否因 CRD/资源类型不存在（KubeVirt 未安装）。
func isKubevirtNotFound(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "the server doesn't have a resource type") ||
		strings.Contains(lower, "no matches for kind") ||
		strings.Contains(lower, "couldn't find resource") ||
		strings.Contains(lower, "not found")
}

// parseVMIs 解析 kubectl get vmi -A -o json 为精简列表。
func parseVMIs(raw string) ([]map[string]interface{}, error) {
	var res struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				Namespace         string `json:"namespace"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				Phase      string `json:"phase"`
				Node       string `json:"nodeName"`
				Interfaces []struct {
					IPAddress string `json:"ipAddress"`
				} `json:"interfaces"`
			} `json:"status"`
			Spec struct {
				Domain struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
						Limits   map[string]string `json:"limits"`
					} `json:"resources"`
				} `json:"domain"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &res) != nil {
		return nil, fmt.Errorf("parse vmi failed")
	}
	out := []map[string]interface{}{}
	for _, it := range res.Items {
		ip := ""
		for _, iface := range it.Status.Interfaces {
			if iface.IPAddress != "" {
				ip = iface.IPAddress
				break
			}
		}
		cpu := it.Spec.Domain.Resources.Requests["cpu"]
		mem := it.Spec.Domain.Resources.Requests["memory"]
		if cpu == "" {
			cpu = it.Spec.Domain.Resources.Limits["cpu"]
		}
		if mem == "" {
			mem = it.Spec.Domain.Resources.Limits["memory"]
		}
		out = append(out, map[string]interface{}{
			"name":       it.Metadata.Name,
			"namespace":  it.Metadata.Namespace,
			"status":     it.Status.Phase,
			"node":       it.Status.Node,
			"ip":         ip,
			"cpu":        cpu,
			"memory":     mem,
			"created_at": it.Metadata.CreationTimestamp,
		})
	}
	return out, nil
}
