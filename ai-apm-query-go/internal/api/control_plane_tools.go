package api

import (
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// controlPlaneToolsPrefix 是 control-plane 工具子域路径前缀。
const controlPlaneToolsPrefix = "/internal/v1/control-plane/tools"

// InternalControlPlaneToolRouter 分发 /internal/v1/control-plane/tools/*。
// 当前提供 27.14 Evidence 一次消费端点（/tools/{id}/evidence/consume）。
func (h *Handler) InternalControlPlaneToolRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if !strings.HasPrefix(path, controlPlaneToolsPrefix) {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	rest := strings.TrimPrefix(path, controlPlaneToolsPrefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 3 && parts[1] == "evidence" && parts[2] == "consume" {
		h.internalControlPlaneToolEvidenceConsume(w, r, parts[0])
		return
	}
	respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
}
