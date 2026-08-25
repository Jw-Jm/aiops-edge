package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// internalControlPlaneVerificationAppend persists an independent observer
// result.  The orchestrator supplies evidence snapshots; query-api owns the
// durable row and tenant/run binding.
func (h *Handler) internalControlPlaneVerificationAppend(w http.ResponseWriter, r *http.Request, runID string) {
	_, run, err := h.authorizeControlPlaneForRun(r, "control_plane.verifications.append", "ai-orchestrator", runID)
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		VerificationID           string          `json:"verification_id"`
		ActionID                 string          `json:"action_id"`
		Status                   string          `json:"status"`
		BeforeSnapshot           json.RawMessage `json:"before_snapshot"`
		AfterSnapshot            json.RawMessage `json:"after_snapshot"`
		ObservationWindowSeconds int             `json:"observation_window_seconds"`
		Checks                   json.RawMessage `json:"checks"`
		Summary                  string          `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if body.VerificationID == "" || body.ActionID == "" || !validVerificationStatus(body.Status) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if body.ObservationWindowSeconds <= 0 {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "INVALID_OBSERVATION_WINDOW"})
		return
	}
	derivedStatus, err := deriveVerificationStatus(body.Checks)
	if err != nil || derivedStatus != strings.ToLower(strings.TrimSpace(body.Status)) {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error": "VERIFICATION_STATUS_NOT_DERIVED", "derived_status": derivedStatus,
		})
		return
	}
	now := time.Now()
	created, err := h.verificationDAO.Create(store.AIVerification{
		VerificationID: body.VerificationID, RunID: runID, ActionID: body.ActionID,
		TenantID: run.TenantID, ClusterID: run.PrimaryClusterID, Status: body.Status,
		BeforeSnapshot: body.BeforeSnapshot, AfterSnapshot: body.AfterSnapshot,
		ObservationWindowSeconds: body.ObservationWindowSeconds, Checks: body.Checks,
		Summary: body.Summary, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "verification_persist_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"verification_id": body.VerificationID, "created": created, "replayed": !created, "status": body.Status,
	})
}

// deriveVerificationStatus is the server-owned Verification Policy V1. The
// orchestrator may provide observations, but cannot elevate an observation to
// passed unless the checks deterministically prove it.
func deriveVerificationStatus(raw json.RawMessage) (string, error) {
	var checks []struct {
		EffectSize *float64 `json:"effect_size"`
		SideEffect *bool    `json:"side_effect"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &checks) != nil || len(checks) == 0 {
		return "inconclusive", nil
	}
	seen := false
	for _, check := range checks {
		if check.SideEffect != nil && *check.SideEffect {
			return "regressed", nil
		}
		if check.EffectSize == nil {
			continue
		}
		seen = true
		if *check.EffectSize <= 0 {
			return "failed", nil
		}
	}
	if !seen {
		return "inconclusive", nil
	}
	return "passed", nil
}

func validVerificationStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed", "failed", "regressed", "inconclusive":
		return true
	default:
		return false
	}
}
