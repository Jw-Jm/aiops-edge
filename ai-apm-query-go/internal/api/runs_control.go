package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
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
	// P0#1：Public Cancel 收敛到 RunControlService.CancelTx（唯一权威）。
	// 与 Internal/Admin Cancel 相同：原子 set cancelled + state_version++ +
	// lease_epoch++ / clear lease（旧 executor 被 Fence）+ append RUN_CANCELLED event +
	// command 幂等响应。不再走 runDAO.Cancel 非原子路径。
	if h.runControl == nil {
		h.runControl = &RunControlService{runDAO: h.runDAO, cmdDAO: h.cmdDAO, eventDAO: h.eventDAO}
	}
	payloadHash := controlCommandPayloadHash(runID, "cancel", &run.StateVersion, "cancelled")
	res := h.runControl.CancelTx(runID, body.CommandID, payloadHash, run.StateVersion, auth.UserID)
	if res.Error != nil {
		if errors.Is(res.Error, store.ErrRunTerminal) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunCancelled})
			return
		}
		if errors.Is(res.Error, store.ErrRunNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
			return
		}
		var cvc *cancelVersionConflictError
		if errors.As(res.Error, &cvc) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunCancelled})
			return
		}
		var cir *cancelIdempotencyReusedError
		if errors.As(res.Error, &cir) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "IDEMPOTENCY_KEY_REUSED"})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_cancel_failed"})
		return
	}
	updated, _ := h.runDAO.Get(runID)
	respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(updated)})
}
