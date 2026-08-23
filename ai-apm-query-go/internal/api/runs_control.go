package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — 公共 Control 入口（P10 完整闭环 Plan D）。
//
// Browser → query-api 公共 POST /api/v1/ai/runs/{id}/cancel（JWT + tenant + capability
// 校验）。取消是显式 control action；同时幂等写 ai_control_commands（command_id）。
// ─────────────────────────────────────────────────────────────────────────────

// PublicCancelRun handles POST /api/v1/ai/runs/{id}/cancel。
func (h *Handler) PublicCancelRun(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	runID := extractRunIDFromPath(r.URL.Path)
	if runID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	var body struct {
		CommandID string `json:"command_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	// 幂等写 control command（command_id 唯一）。
	if body.CommandID != "" {
		_ = h.recordControlCommand(runID, "cancel", body.CommandID)
	}
	ok, err = h.runDAO.Cancel(runID, run.StateVersion, time.Now())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_cancel_failed"})
		return
	}
	if !ok {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunCancelled})
		return
	}
	updated, _ := h.runDAO.Get(runID)
	respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(updated)})
}
