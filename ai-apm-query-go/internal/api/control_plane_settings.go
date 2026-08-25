package api

import (
	"encoding/json"
	"net/http"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const recoveryPolicySettingKey = "recovery_policy"

// InternalControlPlaneRecoveryPolicy handles the orchestrator-owned recovery
// policy without allowing the orchestrator to connect to MySQL directly.
//
// GET/PUT are both protected by distinct control-plane capabilities so a
// caller that can evaluate a policy cannot silently change it.
func (h *Handler) InternalControlPlaneRecoveryPolicy(w http.ResponseWriter, r *http.Request) {
	capability := "control_plane.settings.read"
	if r.Method == http.MethodPut {
		capability = "control_plane.settings.write"
	}
	if _, err := authorizeInternalControlPlane(r, capability, "ai-orchestrator"); err != nil {
		respondInternalQueryError(w, err)
		return
	}

	dao := &store.SettingDAO{}
	switch r.Method {
	case http.MethodGet:
		value, err := dao.Get(recoveryPolicySettingKey)
		if err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "settings_unavailable"})
			return
		}
		if value == "" {
			respondJSON(w, http.StatusOK, map[string]interface{}{"policy": nil})
			return
		}
		var policy map[string]interface{}
		if err := json.Unmarshal([]byte(value), &policy); err != nil || policy == nil {
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "invalid_recovery_policy"})
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"policy": policy})
	case http.MethodPut:
		var body struct {
			Policy map[string]interface{} `json:"policy"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := decoder.Decode(&body); err != nil || body.Policy == nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
			return
		}
		value, err := json.Marshal(body.Policy)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
			return
		}
		if err := dao.Set(recoveryPolicySettingKey, string(value)); err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "settings_unavailable"})
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		w.Header().Set("Allow", "GET, PUT")
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
	}
}
