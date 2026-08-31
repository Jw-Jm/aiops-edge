package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// GenerateChatReport builds a deterministic report from the MySQL-owned chat
// transcript.  The production browser path must not proxy the legacy
// orchestrator SQLite report endpoint (which would reintroduce a second data
// owner).  LLM narrative remains available in the chat stream itself; this
// endpoint provides a durable, scope-checked export that is safe when the LLM
// is unavailable.
func (h *Handler) GenerateChatReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, ok := writeChatSessionAuth(w, r)
	if !ok {
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Service   string `json:"service"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "VALIDATION_FAILED"})
		return
	}
	body.SessionID = strings.TrimSpace(body.SessionID)
	if !canonicalUUID.MatchString(body.SessionID) {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_session_id"})
		return
	}
	item, messages, err := (&store.AIChatSessionDAO{}).Get(body.SessionID, ctx.UserID, ctx.TenantID, ctx.ActiveClusterID)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
		return
	}
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	service := strings.TrimSpace(body.Service)
	if service == "" {
		service = item.Service
	}
	var b strings.Builder
	b.WriteString("# AIOps 对话报告\n\n")
	b.WriteString("> 本报告基于 Query API 持久化的对话证据生成；未从浏览器或旧 Orchestrator 状态读取数据。\n\n")
	b.WriteString("- 会话 ID: `" + item.SessionID + "`\n")
	b.WriteString("- 服务: `" + service + "`\n")
	b.WriteString("- 集群: `" + ctx.ActiveClusterID + "`\n")
	b.WriteString("- 更新时间: `" + item.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z") + "`\n\n")
	b.WriteString("## 对话证据\n\n")
	for _, msg := range messages {
		role := msg.Role
		if role == "user" {
			role = "用户"
		} else if role == "assistant" {
			role = "助手"
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" && len(msg.Metadata) > 0 {
			raw, _ := json.Marshal(msg.Metadata)
			content = string(raw)
		}
		if content == "" {
			continue
		}
		if len(content) > 4000 {
			content = content[:4000] + "…"
		}
		b.WriteString("### " + role + "\n\n" + content + "\n\n")
		if b.Len() > 20000 {
			b.WriteString("（报告已按安全上限截断）\n")
			break
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"report": b.String(), "session_id": item.SessionID})
}

func (h *Handler) ChatSessionRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.GetChatSession(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		h.DeleteChatSession(w, r)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// writeChatSessionAuth keeps all session endpoints fail-closed when the active
// MySQL scope is missing.
func writeChatSessionAuth(w http.ResponseWriter, r *http.Request) (AuthorizationContext, bool) {
	ctx, ok := requestAuthorizationContext(r)
	if !ok {
		var err error
		ctx, err = RequestAuthorizationContext(r)
		if err != nil {
			respondAuthorizationError(w, err)
			return AuthorizationContext{}, false
		}
	}
	if ctx.UserID == "" || !canonicalUUID.MatchString(ctx.TenantID) || !canonicalUUID.MatchString(ctx.ActiveClusterID) {
		respondJSON(w, http.StatusConflict, map[string]any{"error": "SCOPE_SELECTION_REQUIRED"})
		return AuthorizationContext{}, false
	}
	return ctx, true
}

func (h *Handler) ListChatSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, ok := writeChatSessionAuth(w, r)
	if !ok {
		return
	}
	items, err := (&store.AIChatSessionDAO{}).List(ctx.UserID, ctx.TenantID, ctx.ActiveClusterID, 50)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (h *Handler) GetChatSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, ok := writeChatSessionAuth(w, r)
	if !ok {
		return
	}
	sid := strings.TrimPrefix(r.URL.Path, "/api/v1/ai/session/")
	if !canonicalUUID.MatchString(sid) {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_session_id"})
		return
	}
	item, messages, err := (&store.AIChatSessionDAO{}).Get(sid, ctx.UserID, ctx.TenantID, ctx.ActiveClusterID)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
		return
	}
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msg := map[string]any{"id": m.ID, "role": m.Role, "content": m.Content}
		if m.Kind != "" {
			msg["kind"] = m.Kind
		}
		for key, value := range m.Metadata {
			msg[key] = value
		}
		out = append(out, msg)
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"session_id": item.SessionID, "intent": item.Intent, "service": item.Service, "messages": out,
	})
}

func (h *Handler) DeleteChatSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, ok := writeChatSessionAuth(w, r)
	if !ok {
		return
	}
	sid := strings.TrimPrefix(r.URL.Path, "/api/v1/ai/session/")
	if !canonicalUUID.MatchString(sid) {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_session_id"})
		return
	}
	deleted, err := (&store.AIChatSessionDAO{}).Delete(sid, ctx.UserID, ctx.TenantID, ctx.ActiveClusterID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	if !deleted {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"message": "session deleted", "session_id": sid})
}

func (h *Handler) ClearChatSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, ok := writeChatSessionAuth(w, r)
	if !ok {
		return
	}
	count, err := (&store.AIChatSessionDAO{}).Clear(ctx.UserID, ctx.TenantID, ctx.ActiveClusterID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"message": "sessions cleared", "deleted": count})
}
