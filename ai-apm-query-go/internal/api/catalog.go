package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// CatalogRouter 分发 /api/v1/catalog/services 下的 CRUD。
// 读操作无需鉴权（登录即可）；写操作由 main 包用 RequireRole("admin") 包装。
func (h *Handler) CatalogRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/catalog/services"
	idStr := strings.TrimPrefix(r.URL.Path, base+"/")
	if idStr == r.URL.Path {
		idStr = ""
	}
	if idStr == "" {
		switch r.Method {
		case http.MethodGet:
			h.catalogList(w, r)
		case http.MethodPost:
			h.catalogCreate(w, r)
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
		h.catalogUpdate(w, r, id)
	case http.MethodDelete:
		h.catalogDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// CatalogListHandler 供 main 包注册读路由。
func (h *Handler) CatalogList(w http.ResponseWriter, r *http.Request) {
	h.catalogList(w, r)
}

func (h *Handler) catalogList(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	items, total, err := (&store.CatalogDAO{}).List(page, size)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"services": []store.ServiceCatalog{}, "total": 0, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"services": items, "total": total})
}

func (h *Handler) catalogCreate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ServiceName string `json:"service_name"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Owner       string `json:"owner"`
		Team        string `json:"team"`
		Tags        string `json:"tags"`
		Status      string `json:"status"`
	}
	json.Unmarshal(body, &req)
	if req.ServiceName == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "service_name required"})
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	id, err := (&store.CatalogDAO{}).Create(&store.ServiceCatalog{
		ServiceName: req.ServiceName, DisplayName: req.DisplayName, Description: req.Description,
		Owner: req.Owner, Team: req.Team, Tags: req.Tags, Status: req.Status,
	})
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true, "id": id})
}

func (h *Handler) catalogUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Owner       string `json:"owner"`
		Team        string `json:"team"`
		Tags        string `json:"tags"`
		Status      string `json:"status"`
	}
	json.Unmarshal(body, &req)
	if err := (&store.CatalogDAO{}).Update(id, &store.ServiceCatalog{
		DisplayName: req.DisplayName, Description: req.Description, Owner: req.Owner,
		Team: req.Team, Tags: req.Tags, Status: req.Status,
	}); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

func (h *Handler) catalogDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := (&store.CatalogDAO{}).Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}
