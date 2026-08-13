package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// SLORouter handles GET/POST /api/v1/slo（列表 + 创建）。
func (h *Handler) SLORouter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSLOs(w, r)
	case http.MethodPost:
		if !isAdmin(w, r) {
			return
		}
		h.createSLO(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// SLORouterByID handles GET/DELETE /api/v1/slo/{id} 与 PUT /api/v1/slo/{id}。
func (h *Handler) SLORouterByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/slo/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "slo id required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getSLO(w, r, id)
	case http.MethodPut:
		if !isAdmin(w, r) {
			return
		}
		h.updateSLO(w, r, id)
	case http.MethodDelete:
		if !isAdmin(w, r) {
			return
		}
		h.deleteSLO(w, r, id)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listSLOs(w http.ResponseWriter, r *http.Request) {
	dao := &store.SLOTargetDAO{}
	list, err := dao.List()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "list slo targets: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"data": list, "total": len(list)})
}

func (h *Handler) getSLO(w http.ResponseWriter, r *http.Request, id string) {
	dao := &store.SLOTargetDAO{}
	s, err := dao.Get(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s == nil {
		respondError(w, http.StatusNotFound, "slo target not found")
		return
	}
	respondJSON(w, http.StatusOK, s)
}

func (h *Handler) createSLO(w http.ResponseWriter, r *http.Request) {
	// 先读原始 body 判断客户端是否显式传了 enabled（P1-4：默认启用，显式 false 尊重）
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	var s store.SLOTarget
	if err := json.Unmarshal(raw, &s); err != nil {
		respondError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if s.Name == "" || s.Service == "" {
		respondError(w, http.StatusBadRequest, "name and service required")
		return
	}
	// 创建时强制生成 id，忽略客户端注入的 id，避免覆盖已有目标
	s.ID = generateID()
	if s.SLOType == "" {
		s.SLOType = "availability"
	}
	if s.Target <= 0 {
		s.Target = 99.9
	}
	if s.WindowSeconds <= 0 {
		s.WindowSeconds = 2592000 // 30d
	}
	// P1-4 修复：默认启用，避免 burn_rate 规则因 SLO disabled 永远不触发；
	// 客户端显式传 enabled=false 时尊重用户选择。
	explicitEnabled := map[string]interface{}{}
	_ = json.Unmarshal(raw, &explicitEnabled)
	if _, ok := explicitEnabled["enabled"]; !ok {
		s.Enabled = true
	}
	dao := &store.SLOTargetDAO{}
	if err := dao.Upsert(s); err != nil {
		respondError(w, http.StatusInternalServerError, "create slo: "+err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, s)
}

func (h *Handler) updateSLO(w http.ResponseWriter, r *http.Request, id string) {
	dao := &store.SLOTargetDAO{}
	existing, err := dao.Get(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		respondError(w, http.StatusNotFound, "slo target not found")
		return
	}
	var s store.SLOTarget
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		respondError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	s.ID = id
	if s.SLOType == "" {
		s.SLOType = "availability"
	}
	if s.Target <= 0 {
		s.Target = 99.9
	}
	if s.WindowSeconds <= 0 {
		s.WindowSeconds = 2592000
	}
	if err := dao.Upsert(s); err != nil {
		respondError(w, http.StatusInternalServerError, "update slo: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, s)
}

func (h *Handler) deleteSLO(w http.ResponseWriter, r *http.Request, id string) {
	dao := &store.SLOTargetDAO{}
	if err := dao.Delete(id); err != nil {
		respondError(w, http.StatusInternalServerError, "delete slo: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// isAdmin 校验当前请求角色为 admin（写权限守卫）。
func isAdmin(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, role, _, ok := validateJWT(token)
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if role != "admin" {
		respondError(w, http.StatusForbidden, "admin role required")
		return false
	}
	return true
}
