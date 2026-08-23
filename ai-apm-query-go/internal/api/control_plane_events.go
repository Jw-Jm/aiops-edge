package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — control-plane events 端点（P10 完整闭环 Plan B）。
// append/replay。capability：append=control_plane.events.append，replay=control_plane.events.replay。
// ─────────────────────────────────────────────────────────────────────────────

// internalControlPlaneEventRouter 路由 events 请求（按 query 参数区分 append/replay）。
func (h *Handler) internalControlPlaneEventRouter(w http.ResponseWriter, r *http.Request, runID string) {
	switch r.Method {
	case http.MethodPost:
		h.internalControlPlaneEventAppend(w, r, runID)
	case http.MethodGet:
		h.internalControlPlaneEventReplay(w, r, runID)
	default:
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
	}
}

// internalControlPlaneEventAppend 处理 POST .../runs/{id}/events。
func (h *Handler) internalControlPlaneEventAppend(w http.ResponseWriter, r *http.Request, runID string) {
	// P0-3：签名 tenant 绑定到目标 Run，防跨租户写事件。
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.events.append", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		EventID   string          `json:"event_id"`
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if body.EventID == "" || body.EventType == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	ev, created, err := h.eventDAO.Append(store.AIRunEvent{
		EventID: body.EventID, RunID: runID, EventType: body.EventType,
		Payload: body.Payload,
	})
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "event_append_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"sequence": ev.Sequence,
		"created":  created,
		"event_id": ev.EventID,
	})
}

// internalControlPlaneEventReplay 处理 GET .../runs/{id}/events?after_sequence=。
func (h *Handler) internalControlPlaneEventReplay(w http.ResponseWriter, r *http.Request, runID string) {
	// P0-3：签名 tenant 绑定到目标 Run，防跨租户读事件。
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.events.replay", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	afterSeq := int64(0)
	if v := r.URL.Query().Get("after_sequence"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			afterSeq = n
		}
	}
	evs, err := h.eventDAO.ReplayAfter(runID, afterSeq)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "event_replay_failed"})
		return
	}
	out := make([]map[string]interface{}, 0, len(evs))
	for _, e := range evs {
		out = append(out, map[string]interface{}{
			"sequence":   e.Sequence,
			"event_id":   e.EventID,
			"event_type": e.EventType,
			"payload":    string(e.Payload),
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"events": out, "total": len(out)})
}
