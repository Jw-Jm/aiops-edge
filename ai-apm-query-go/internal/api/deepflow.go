package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
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
				"deepflow_url": "http://localhost:30417",
				"grafana_url":  "http://localhost:31023",
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

		respondJSON(w, 200, map[string]interface{}{
			"status":       "available",
			"http_status":  resp.StatusCode,
			"version":      version,
			"deepflow_url": "http://localhost:30417",
			"grafana_url":  "http://localhost:31023",
		})
		return
	}
	respondJSON(w, 200, map[string]interface{}{
		"status":   "not_available",
		"message":  "DeepFlow 未响应",
	})
}
