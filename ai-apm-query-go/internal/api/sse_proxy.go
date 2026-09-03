package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — 公共 SSE proxy（P10 完整闭环 Plan D）。
//
// Browser → query-api 公共 SSE（JWT 授权）。query-api 是 ai_run_events 持久化 +
// replay owner，直接从持久化事件 replay + live-tail（**不回到 orchestrator**，R5）。
//
// 授权：AuthMiddleware（JWT + tenant）+ Run tenant/cluster 归属校验；每次重连重新鉴权。
// 语义：Last-Event-ID（sequence）→ replay；heartbeat 12s；retention 越界 → SSE 错误帧。
// ─────────────────────────────────────────────────────────────────────────────

const (
	sseHeartbeatInterval = 12 * time.Second
	ssePollInterval      = 2 * time.Second
	sseRetentionWindow   = int64(10000) // 允许回放的最大 sequence 窗口
)

// StreamRunEvents handles GET /api/v1/ai/runs/{run_id}/events（SSE 流）。
func (h *Handler) StreamRunEvents(w http.ResponseWriter, r *http.Request) {
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
	// Run tenant 归属校验（每次重连重新鉴权）。
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	// Last-Event-ID（sequence）。
	afterSeq := int64(0)
	if leid := r.Header.Get("Last-Event-ID"); leid != "" {
		if n, err := strconv.ParseInt(leid, 10, 64); err == nil {
			afterSeq = n
		}
	}
	if v := r.URL.Query().Get("after_sequence"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			afterSeq = n
		}
	}

	// SSE 响应头。
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	// 服务器全局 WriteTimeout（bootstrap 设 5min）会掐断长连 SSE；按 heartbeat
	// 节奏续期写 deadline（R4）：普通路由保持兜底超时，SSE 由活动自己续命。
	rc := http.NewResponseController(w)
	extendWriteDeadline := func() {
		_ = rc.SetWriteDeadline(time.Now().Add(2 * sseHeartbeatInterval))
	}
	extendWriteDeadline()

	// P1-5：retention 在 replay **前**检查——过期 cursor 立即拒绝（不先全量 replay 再发现超窗）。
	last, lastErr := h.eventDAO.LastSequence(runID)
	if lastErr != nil {
		writeSSE(w, "error", map[string]interface{}{"error": "event_replay_failed"})
		flusher.Flush()
		return
	}
	if afterSeq < last-sseRetentionWindow {
		writeSSE(w, "error", map[string]interface{}{"error": "SSE_RETENTION_EXCEEDED", "last": last})
		flusher.Flush()
		return
	}

	// replay 已持久化事件。
	evs, err := h.eventDAO.ReplayAfter(runID, afterSeq)
	if err != nil {
		writeSSE(w, "error", map[string]interface{}{"error": "event_replay_failed"})
		flusher.Flush()
		return
	}
	for _, e := range evs {
		writeSSE(w, "run_event", map[string]interface{}{
			"sequence": e.Sequence, "event_id": e.EventID, "event_type": e.EventType,
			"payload": json.RawMessage(e.Payload),
		})
	}
	if flusher != nil {
		flusher.Flush()
	}
	if len(evs) > 0 {
		afterSeq = evs[len(evs)-1].Sequence
	}
	extendWriteDeadline()

	// live-tail：轮询 LastSequence，heartbeat。
	lastHeartbeat := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		last, err := h.eventDAO.LastSequence(runID)
		if err == nil {
			if last > afterSeq {
				evs, _ := h.eventDAO.ReplayAfter(runID, afterSeq)
				for _, e := range evs {
					writeSSE(w, "run_event", map[string]interface{}{
						"sequence": e.Sequence, "event_id": e.EventID, "event_type": e.EventType,
						"payload": json.RawMessage(e.Payload),
					})
					if e.Sequence > afterSeq {
						afterSeq = e.Sequence
					}
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			// retention 越界：若浏览器落后超过窗口，发错误帧。
			if afterSeq < last-sseRetentionWindow {
				writeSSE(w, "error", map[string]interface{}{"error": "SSE_RETENTION_EXCEEDED"})
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
		}
		if time.Since(lastHeartbeat) >= sseHeartbeatInterval {
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			lastHeartbeat = time.Now()
			extendWriteDeadline()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(ssePollInterval):
		}
	}
}

// writeSSE 写一个 SSE 事件帧。
func writeSSE(w http.ResponseWriter, event string, data map[string]interface{}) {
	raw, _ := json.Marshal(data)
	if sequence, ok := data["sequence"].(int64); ok && sequence > 0 {
		_, _ = fmt.Fprintf(w, "id: %d\n", sequence)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
}

// extractRunIDFromPath 从 /api/v1/ai/runs/{id}/events 提取 run_id。
func extractRunIDFromPath(path string) string {
	const prefix = "/api/v1/ai/runs/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 1 || parts[0] == "" {
		return ""
	}
	return parts[0]
}
