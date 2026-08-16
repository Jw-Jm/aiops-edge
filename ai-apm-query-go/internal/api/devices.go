package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// DeviceRouter 分发 /api/v1/devices 下的 CRUD。
func (h *Handler) DeviceRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/devices"
	idStr := strings.TrimPrefix(r.URL.Path, base+"/")
	if idStr == r.URL.Path {
		idStr = ""
	}
	if idStr == "" {
		switch r.Method {
		case http.MethodGet:
			h.deviceList(w, r)
		case http.MethodPost:
			h.deviceCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	// /devices/{id}/metrics 子路由
	if strings.HasSuffix(idStr, "/metrics") {
		idNum := strings.TrimSuffix(idStr, "/metrics")
		idM, err := strconv.ParseInt(idNum, 10, 64)
		if err != nil {
			http.Error(w, "bad id", 400)
			return
		}
		h.deviceMetrics(w, r, idM)
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.deviceUpdate(w, r, id)
	case http.MethodDelete:
		h.deviceDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// deviceMetrics 返回设备实时指标（从 VM 查询 node-exporter，按 instance 匹配设备 IP）。
func (h *Handler) deviceMetrics(w http.ResponseWriter, r *http.Request, id int64) {
	d := &store.DeviceDAO{}
	dev, err := d.GetByID(id)
	if err != nil || dev == nil {
		respondJSON(w, 404, map[string]interface{}{"error": "device not found"})
		return
	}
	instance := dev.IP
	if instance == "" {
		instance = dev.Hostname
	}
	// 尝试设备 IP:9100，若无数据则 fallback 到通用 node-exporter
	promQLs := map[string]string{
		"cpu_usage":       fmt.Sprintf(`100 - avg(rate(node_cpu_seconds_total{instance="%s:9100",mode="idle"}[5m])) * 100`, instance),
		"memory_usage":    fmt.Sprintf(`100 * (1 - node_memory_MemAvailable_bytes{instance="%s:9100"} / node_memory_MemTotal_bytes{instance="%s:9100"})`, instance, instance),
		"disk_usage":      fmt.Sprintf(`max(100 * (1 - node_filesystem_avail_bytes{instance="%s:9100",mountpoint="/",fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{instance="%s:9100",mountpoint="/",fstype!~"tmpfs|overlay"}))`, instance, instance),
		"load1":           fmt.Sprintf(`node_load1{instance="%s:9100"}`, instance),
		"network_rx_bps":  fmt.Sprintf(`sum(rate(node_network_receive_bytes_total{instance="%s:9100"}[5m]))`, instance),
		"network_tx_bps":  fmt.Sprintf(`sum(rate(node_network_transmit_bytes_total{instance="%s:9100"}[5m]))`, instance),
		"process_count":   fmt.Sprintf(`node_processes{instance="%s:9100"}`, instance),
		"up":              fmt.Sprintf(`up{instance="%s:9100"}`, instance),
	}
	metrics := make(map[string]float64)
	for k, q := range promQLs {
		v, err := h.vmInstantQuery(q)
		if err == nil {
			metrics[k] = v
		}
	}
	// 补充设备元信息
	metrics["cpu_cores"] = float64(dev.CPUCores)
	metrics["memory_mb_total"] = float64(dev.MemoryMB)
	status := 0.0
	if metrics["up"] > 0 {
		status = 1
	}
	respondJSON(w, 200, map[string]interface{}{
		"device": dev.Hostname, "instance": instance, "metrics": metrics, "online": status > 0,
	})
}

func (h *Handler) DeviceList(w http.ResponseWriter, r *http.Request) {
	h.deviceList(w, r)
}

func (h *Handler) deviceList(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	items, total, err := (&store.DeviceDAO{}).List(page, size)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"devices": []store.Device{}, "total": 0, "error": err.Error()})
		return
	}
	// scope 过滤：限定设备范围时，只返回授权设备（按 hostname）
	sc := currentScope(r)
	if !sc.IsFull() {
		filtered := make([]store.Device, 0, len(items))
		for _, d := range items {
			if sc.ContainsDevice(d.Hostname) {
				filtered = append(filtered, d)
			}
		}
		items = filtered
		total = len(filtered)
	}
	respondJSON(w, 200, map[string]interface{}{"devices": items, "total": total})
}

func (h *Handler) deviceCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req store.Device
	json.Unmarshal(body, &req)
	if req.Hostname == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "hostname required"})
		return
	}
	if req.Status == "" {
		req.Status = "online"
	}
	id, err := (&store.DeviceDAO{}).Create(&req)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	auditWrite(r, "device.create", req.Hostname, "新增设备 ip="+req.IP)
	respondJSON(w, 200, map[string]interface{}{"ok": true, "id": id})
}

func (h *Handler) deviceUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	body, _ := io.ReadAll(r.Body)
	var req store.Device
	json.Unmarshal(body, &req)
	if err := (&store.DeviceDAO{}).Update(id, &req); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	auditWrite(r, "device.update", strconv.FormatInt(id, 10), "更新设备 "+req.Hostname)
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) deviceDelete(w http.ResponseWriter, r *http.Request, id int64) {
	d := &store.DeviceDAO{}
	// P3-1 修复：删除不存在的设备返回 404。
	existing, err := d.GetByID(id)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	if existing == nil {
		respondJSON(w, 404, map[string]interface{}{"error": "device not found"})
		return
	}
	if err := d.Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	auditWrite(r, "device.delete", existing.Hostname, "删除设备")
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}
