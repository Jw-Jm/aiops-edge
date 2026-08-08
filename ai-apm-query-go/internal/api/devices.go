package api

import (
	"encoding/json"
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
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) deviceDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := (&store.DeviceDAO{}).Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}
