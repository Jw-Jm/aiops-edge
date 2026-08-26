package api

import (
	"encoding/json"
	"net/http"

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
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "invalid credentials"})
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

	// Keep the response deliberately small: no password or hash is ever
	// echoed, and the frontend only needs the rotated token and state.
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"token": token, "must_change_password": false,
	})
}
