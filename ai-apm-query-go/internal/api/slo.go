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
	// P1-4 修复：默认启用，避免 burn_rate 规则因 SLO disabled 永远不触发；
	// 客户端显式传 enabled=false 时尊重用户选择。
	fieldMap := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fieldMap)
	if _, ok := fieldMap["enabled"]; !ok {
		s.Enabled = true
	}
	// P2-1 修复：window_seconds/target 显式非法值返回 400，而非静默默认。
	if msg := validateSLOInput(&s, fieldMap); msg != "" {
		respondError(w, http.StatusBadRequest, msg)
		return
	}
	dao := &store.SLOTargetDAO{}
	if err := dao.Upsert(s); err != nil {
		respondError(w, http.StatusInternalServerError, "create slo: "+err.Error())
		return
	}
	auditWrite(r, "slo.create", s.Name, "创建 SLO 目标 service="+s.Service)
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
	s.ID = id
	fieldMap := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fieldMap)
	// P2-1 修复：显式非法值（负数/超界）返回 400，不再静默默认。
	if msg := validateSLOInput(&s, fieldMap); msg != "" {
		respondError(w, http.StatusBadRequest, msg)
		return
	}
	if err := dao.Upsert(s); err != nil {
		respondError(w, http.StatusInternalServerError, "update slo: "+err.Error())
		return
	}
	auditWrite(r, "slo.update", id, "更新 SLO 目标 "+s.Name)
	respondJSON(w, http.StatusOK, s)
}

// validateSLOInput 校验 SLO 输入并应用缺省值，返回空串表示通过。
// 安全(P2-1)：window_seconds 必须落在 [3600, 7776000]（1h~90d），
// 显式负数/超界直接 400，不再静默回退默认值；target 按类型限界：
// availability ∈ (0,100]、latency > 0（仅显式传入时校验，未传保留默认行为）。
func validateSLOInput(s *store.SLOTarget, raw map[string]interface{}) string {
	if s.SLOType == "" {
		s.SLOType = "availability"
	}
	if s.SLOType != "availability" && s.SLOType != "latency" {
		return "slo_type must be availability or latency"
	}
	if _, ok := raw["window_seconds"]; ok {
		if s.WindowSeconds < 3600 || s.WindowSeconds > 7776000 {
			return "window_seconds must be between 3600 and 7776000 (1h~90d)"
		}
	} else if s.WindowSeconds <= 0 {
		s.WindowSeconds = 2592000 // 30d
	}
	if _, ok := raw["target"]; ok {
		if s.SLOType == "availability" {
			if s.Target <= 0 || s.Target > 100 {
				return "target must be in (0, 100] for availability SLO"
			}
		} else {
			if s.Target <= 0 {
				return "target must be > 0 for latency SLO"
			}
		}
	} else if s.Target <= 0 {
		s.Target = 99.9
	}
	return ""
}

func (h *Handler) deleteSLO(w http.ResponseWriter, r *http.Request, id string) {
	dao := &store.SLOTargetDAO{}
	// P3-1 修复：删除不存在的 SLO 目标返回 404。
	existing, err := dao.Get(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		respondError(w, http.StatusNotFound, "slo target not found")
		return
	}
	if err := dao.Delete(id); err != nil {
		respondError(w, http.StatusInternalServerError, "delete slo: "+err.Error())
		return
	}
	auditWrite(r, "slo.delete", existing.Name, "删除 SLO 目标")
	respondJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// isAdmin fences legacy privileged handlers until they are migrated to a
// canonical MySQL AuthorizationDAO action and resource scope. It deliberately
// does not parse a JWT role claim.
func isAdmin(w http.ResponseWriter, r *http.Request) bool {
	respondError(w, http.StatusForbidden, "permission_denied")
	return false
}
