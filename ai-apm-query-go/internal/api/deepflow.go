package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func (h *Handler) DeepFlowStatus(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 5 * time.Second}

	// Try DeepFlow app health
	resp, err := client.Get("http://deepflow-app.deepflow.svc.cluster.local:20418/")
	if err != nil {
		resp, err = client.Get("http://deepflow-server.deepflow.svc.cluster.local:20416/v1/health/")
		if err != nil {
			log.Printf("DeepFlow check failed: %v", err)
			respondJSON(w, 200, map[string]interface{}{
				"status":   "not_available",
				"message":  "DeepFlow 服务不可达",
				"deepflow_url": deepflowUIURL(),
				"grafana_url":  deepflowGrafanaURL(),
			})
			return
		}
	}
	if resp != nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		// Try parse as JSON for version info
		var info map[string]interface{}
		version := ""
		if json.Unmarshal(body, &info) == nil {
			if v, ok := info["version"]; ok { version = v.(string) }
		}

		ui := deepflowUIURL()
		grafana := deepflowGrafanaURL()
		// P1-5 修复：服务可达但对外访问地址未配置时，不得报 available（
		// 此前 URL 恒为空却返回 available，前端据此展示"已接入"误导用户）。
		if ui == "" && grafana == "" {
			respondJSON(w, 200, map[string]interface{}{
				"status":      "not_configured",
				"message":     "DeepFlow 服务可达但未配置对外访问地址（DEEPFLOW_UI_URL/DEEPFLOW_GRAFANA_URL）",
				"http_status": resp.StatusCode,
				"version":     version,
				"deepflow_url": ui,
				"grafana_url":  grafana,
			})
			return
		}

		respondJSON(w, 200, map[string]interface{}{
			"status":       "available",
			"http_status":  resp.StatusCode,
			"version":      version,
			"deepflow_url": ui,
			"grafana_url":  grafana,
		})
		return
	}
	respondJSON(w, 200, map[string]interface{}{
		"status":   "not_available",
		"message":  "DeepFlow 未响应",
	})
}

// deepflowUIURL / deepflowGrafanaURL 从环境变量读取 DeepFlow 对外访问地址（可移植，不写死 localhost）。
// 未配置时返回空串，前端用 window.location.hostname 拼默认端口。
func deepflowUIURL() string {
	return os.Getenv("DEEPFLOW_UI_URL")
}

func deepflowGrafanaURL() string {
	return os.Getenv("DEEPFLOW_GRAFANA_URL")
}
