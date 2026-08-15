package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// DashboardRouter handles GET/POST /api/v1/dashboard/panels（列表 + 创建）。
func (h *Handler) DashboardRouter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listPanels(w, r)
	case http.MethodPost:
		if !isAdmin(w, r) {
			return
		}
		h.createPanel(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// DashboardRouterByID handles PUT/DELETE /api/v1/dashboard/panels/{id}。
func (h *Handler) DashboardRouterByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/dashboard/panels/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "panel id required")
		return
	}
	switch r.Method {
	case http.MethodPut:
		if !isAdmin(w, r) {
			return
		}
		h.updatePanel(w, r, id)
	case http.MethodDelete:
		if !isAdmin(w, r) {
			return
		}
		h.deletePanel(w, r, id)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listPanels(w http.ResponseWriter, r *http.Request) {
	dao := &store.DashboardPanelDAO{}
	list, err := dao.List()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "list panels: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"data": list, "total": len(list)})
}

func (h *Handler) createPanel(w http.ResponseWriter, r *http.Request) {
	var p store.DashboardPanel
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		respondError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if p.Title == "" || p.Query == "" {
		respondError(w, http.StatusBadRequest, "title and query required")
		return
	}
	p.ID = generateID()
	if p.ChartType == "" {
		p.ChartType = "line"
	}
	dao := &store.DashboardPanelDAO{}
	if err := dao.Upsert(p); err != nil {
		respondError(w, http.StatusInternalServerError, "create panel: "+err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, p)
}

func (h *Handler) updatePanel(w http.ResponseWriter, r *http.Request, id string) {
	dao := &store.DashboardPanelDAO{}
	existing, err := dao.Get(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		respondError(w, http.StatusNotFound, "panel not found")
		return
	}
	var p store.DashboardPanel
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		respondError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	p.ID = id
	if p.ChartType == "" {
		p.ChartType = "line"
	}
	if err := dao.Upsert(p); err != nil {
		respondError(w, http.StatusInternalServerError, "update panel: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}

func (h *Handler) deletePanel(w http.ResponseWriter, r *http.Request, id string) {
	dao := &store.DashboardPanelDAO{}
	// P3-1 修复：删除不存在的面板返回 404。
	existing, err := dao.Get(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		respondError(w, http.StatusNotFound, "panel not found")
		return
	}
	if err := dao.Delete(id); err != nil {
		respondError(w, http.StatusInternalServerError, "delete panel: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"deleted": id})
}
