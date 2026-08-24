package api

import (
	"encoding/json"
	"net/http"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// ─────────────────────────────────────────────────────────────────────────────
// A2-01：共享 TrustedRequest replay guard 显式消费端点。
//
// POST /internal/v1/security/replay/consume
//   - 用 TrustedRequestContext V2（system principal，capability=control_plane.replay.consume）
//     认证（consumer_service 只能来自认证服务身份）。
//   - body: {issuer, audience, nonce, ttl_seconds}
//   - 首次消费（nonce 不存在）→ 200 {consumed:true}；重复 → 409 context_replayed。
//
// 与验证器内部的 MySQLReplayCache.CheckAndStore 共用 ai_context_replay_guard 表；
// 本端点供需要"显式先占后用"的调用方（如跨方向的 RunInvocation/RunControl 共享 context）。
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) SecurityReplayConsume(w http.ResponseWriter, r *http.Request) {
	// 认证：system principal + 独立 capability（不借用 control_plane.runs.mutate）。
	if _, err := authorizeInternalControlPlane(r, "control_plane.replay.consume", "ai-orchestrator"); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		Issuer      string `json:"issuer"`
		Audience    string `json:"audience"`
		Nonce       string `json:"nonce"`
		TTLSeconds  int    `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "INVALID_BODY"})
		return
	}
	if body.Issuer == "" || body.Audience == "" || body.Nonce == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_REPLAY_FIELDS"})
		return
	}
	ttl := body.TTLSeconds
	if ttl <= 0 || ttl > 3600 {
		ttl = 60
	}
	created, err := ConsumeReplayNonce(body.Issuer, body.Audience, body.Nonce, ttl)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "replay_consume_failed"})
		return
	}
	if !created {
		cp.inc("replay_rejected")
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeContextReplayed})
		return
	}
	cp.inc("replay_consumed")
	respondJSON(w, http.StatusOK, map[string]interface{}{"consumed": true})
}
