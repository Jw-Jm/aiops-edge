package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
)

func (h *Handler) SystemStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"cache":  GetCacheStats(),
		// Redis 已移除：缓存为纯内存实现（orchestrator 的 ARQ 任务队列才用 Redis）
		"redis":  "in-memory",
		"hpa":    getHPAStatus(),
		"pods":   getPodCount(),
	}

	respondJSON(w, 200, map[string]interface{}{"status": status})
}

func (h *Handler) CacheStats(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, 200, map[string]interface{}{
		"cache": GetCacheStats(),
		// Redis 已移除：缓存为纯内存实现（orchestrator 的 ARQ 任务队列才用 Redis）
		"redis": map[string]interface{}{
			"connected": true,
			"url":       "in-memory",
		},
	})
}

func (h *Handler) InvalidateCache(w http.ResponseWriter, r *http.Request) {
	pattern := r.URL.Query().Get("pattern")
	if pattern == "" {
		pattern = "cache:"
	}
	InvalidateCache(pattern)
	respondJSON(w, 200, map[string]interface{}{
		"invalidated": true,
		"pattern":     pattern,
	})
}

func getHPAStatus() map[string]interface{} {
	result := map[string]interface{}{}
	data, err := exec.Command("kubectl", "get", "hpa", "-n", "observability", "-o", "json").Output()
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	var hpaList struct {
		Items []struct {
			Metadata struct{ Name string }
			Status   struct {
				CurrentReplicas int `json:"currentReplicas"`
				DesiredReplicas int `json:"desiredReplicas"`
				CurrentMetrics  []struct {
					Type   string
					Resource struct {
						Name string
						Current struct {
							AverageUtilization int    `json:"averageUtilization"`
							AverageValue       string `json:"averageValue"`
						} `json:"current"`
					} `json:"resource"`
				} `json:"currentMetrics"`
			} `json:"status"`
		} `json:"items"`
	}
	json.Unmarshal(data, &hpaList)
	for _, item := range hpaList.Items {
		metrics := map[string]interface{}{}
		for _, m := range item.Status.CurrentMetrics {
			metrics[m.Resource.Name] = m.Resource.Current.AverageUtilization
		}
		result[item.Metadata.Name] = map[string]interface{}{
			"current": item.Status.CurrentReplicas,
			"desired": item.Status.DesiredReplicas,
			"metrics": metrics,
		}
	}
	return result
}

func getPodCount() map[string]interface{} {
	data, err := exec.Command("kubectl", "get", "pods", "-n", "observability", "--no-headers").Output()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	running := 0
	for _, line := range lines {
		if strings.Contains(line, "Running") && !strings.Contains(line, "Terminating") {
			running++
		}
	}
	return map[string]interface{}{"total": len(lines), "running": running}
}
