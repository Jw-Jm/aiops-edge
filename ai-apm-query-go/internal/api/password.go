package api

import (
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ChangePassword changes the authenticated user's password. The current
// password is checked again even though a valid session exists, which makes a
// stolen first-login token insufficient to silently set a replacement secret.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authorization, ok := requestAuthorizationContext(r)
	if !ok || authorization.UserID == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid JSON"})
		return
	}
	if len([]rune(req.NewPassword)) < 8 {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "new password must be at least 8 characters"})
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "new passwords do not match"})
		return
	}
	if req.CurrentPassword == req.NewPassword {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "new password must differ from current password"})
		return
	}

	dao := &store.UserDAO{}
	passwordHash, _, err := dao.GetPasswordStateByUUID(authorization.UserID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
		return
	}
	if passwordHash == "" || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.CurrentPassword)) != nil {
		// PF-PAGE-002：错误的"当前密码"是用户输入错误（客户端校验失败），返回 400
		// + invalid_current_password。401 仅保留给会话失效/未认证场景——此前这里
		// 返回 401 会被前端全局拦截器误判为会话过期而强制登出。
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_current_password"})
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "password hash failed"})
		return
	}
	if err := dao.ChangePassword(authorization.UserID, string(newHash)); err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
		return
	}
	token, err := issueSessionToken(authorization.UserID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
		return
	}

	// Rotate the browser session in the same HttpOnly cookie used by login.  The
	// credential never appears in JSON, which prevents JavaScript from reading
	// or persisting a bearer token.
	http.SetCookie(w, &http.Cookie{
		Name: "aiops_access", Value: token, Path: "/", MaxAge: 24 * 60 * 60,
		Expires:  time.Now().UTC().Add(24 * time.Hour),
		HttpOnly: true, Secure: secureSessionCookie(), SameSite: http.SameSiteLaxMode,
	})
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": true, "must_change_password": false,
	})
}
