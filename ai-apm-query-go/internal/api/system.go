package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func (h *Handler) SystemStatus(w http.ResponseWriter, r *http.Request) {
	// G3 修复（S6）：系统状态含集群拓扑/缓存/HPA 信息，仅 admin 可访问。
	if !hasRole(r, "admin") {
		respondJSON(w, 403, map[string]interface{}{"error": "forbidden: admin role required"})
		return
	}
	status := map[string]interface{}{
		"cache": GetCacheStats(),
		"hpa":   getHPAStatus(),
		"pods":  getPodCount(),
	}

	respondJSON(w, 200, map[string]interface{}{"status": status})
}

func (h *Handler) CacheStats(w http.ResponseWriter, r *http.Request) {
	// G3 修复（S6）：缓存统计仅 admin 可访问。
	if !hasRole(r, "admin") {
		respondJSON(w, 403, map[string]interface{}{"error": "forbidden: admin role required"})
		return
	}
	respondJSON(w, 200, map[string]interface{}{
		"cache": GetCacheStats(),
	})
}

func (h *Handler) InvalidateCache(w http.ResponseWriter, r *http.Request) {
	// G3 修复（S6）：缓存刷新仅 admin 可访问。
	if !hasRole(r, "admin") {
		respondJSON(w, 403, map[string]interface{}{"error": "forbidden: admin role required"})
		return
	}
	// G3 修复（S6）：移除任意 pattern 能力——仅允许精确 key 匹配，
	// 防止用空/宽泛 pattern 反复清空缓存造成 DoS。key 必须与缓存条目完全一致。
	key := r.URL.Query().Get("key")
	if key == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "key required (exact cache key)"})
		return
	}
	if !InvalidateCacheExact(key) {
		respondJSON(w, 404, map[string]interface{}{"error": "cache key not found"})
		return
	}
	respondJSON(w, 200, map[string]interface{}{
		"invalidated": true,
		"key":         key,
	})
}

// SystemComponents 处理 GET /api/v1/system/components — 各组件探活结果列表。
// 组件列表：query-api/ingest/ai-orchestrator/clickhouse/mysql/
// victoria-metrics/victoria-logs/minio/frontend。3s 超时并发探测，
// 探活失败 status=down，超时接近 3s 的降级 degraded。
func (h *Handler) SystemComponents(w http.ResponseWriter, r *http.Request) {
	// G3 修复（S6）：组件探活执行 kubectl 探测内部服务，泄露集群拓扑，仅 admin 可访问。
	if !hasRole(r, "admin") {
		respondJSON(w, 403, map[string]interface{}{"error": "forbidden: admin role required"})
		return
	}
	type compItem struct{ name, typ, kind, addr string }
	components := []compItem{
		{"query-api", "service", "http", "http://query-api.observability.svc.cluster.local:8080/health"},
		{"ingest", "service", "http", "http://ingest.observability.svc.cluster.local:8080/health"},
		{"ai-orchestrator", "service", "http", "http://ai-orchestrator.observability.svc.cluster.local:8080/health"},
		{"clickhouse", "middleware", "tcp", "clickhouse.observability.svc.cluster.local:8123"},
		{"mysql", "middleware", "tcp", "mysql.observability.svc.cluster.local:3306"},
		{"victoria-metrics", "middleware", "http", "http://victoria-metrics.observability.svc.cluster.local:8428/health"},
		{"victoria-logs", "middleware", "http", "http://victoria-logs.observability.svc.cluster.local:9428/health"},
		{"minio", "middleware", "http", "http://minio.observability.svc.cluster.local:9000/minio/health/live"},
		{"frontend", "service", "http", "http://frontend.observability.svc.cluster.local/health"},
	}

	results := make([]map[string]interface{}, len(components))
	var wg sync.WaitGroup
	for i, c := range components {
		wg.Add(1)
		go func(i int, c compItem) {
			defer wg.Done()
			start := time.Now()
			ok := probeComponent(c.kind, c.addr)
			latency := time.Since(start).Milliseconds()
			status := "ok"
			if !ok {
				status = "down"
			} else if latency >= 2000 {
				status = "degraded"
			}
			detail := ""
			if !ok {
				detail = c.addr
			}
			results[i] = map[string]interface{}{
				"name":       c.name,
				"type":       c.typ,
				"status":     status,
				"latency_ms": latency,
				"detail":     detail,
			}
		}(i, c)
	}
	wg.Wait()

	respondJSON(w, http.StatusOK, map[string]interface{}{"components": results})
}

// probeComponent 按 kind 探测组件：http 用 GET（3s 超时），tcp 用 DialTimeout。
func probeComponent(kind, addr string) bool {
	if kind == "tcp" {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(addr)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return true // 能建立 HTTP 连接即视为可达（不苛求状态码）
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
