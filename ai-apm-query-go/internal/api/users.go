package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// UserList GET /api/v1/users — 用户列表（admin）。
func (h *Handler) UserList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	users, total, err := (&store.UserDAO{}).List(page, size)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"users": []store.User{}, "total": 0, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"users": users, "total": total})
}

// UserCreate POST /api/v1/users — 创建用户（admin）。
func (h *Handler) UserCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Username, Password, DisplayName, Role, Email string
	}
	json.Unmarshal(body, &req)
	if req.Username == "" || req.Password == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "username and password required"})
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": "hash error"})
		return
	}
	u := &store.User{
		Username: req.Username, PasswordHash: string(hash),
		DisplayName: req.DisplayName, Role: req.Role, Email: req.Email, Status: 1,
	}
	id, err := (&store.UserDAO{}).Create(u)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true, "id": id})
}

// UserUpdate PUT /api/v1/users/{id} — 更新用户（admin）。
func (h *Handler) UserUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		http.Error(w, "method not allowed", 405)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "bad id"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		DisplayName, Role, Email string
		Status                   int
		Password                 string
		Scope                    string
	}
	json.Unmarshal(body, &req)
	status := req.Status
	if status == 0 {
		status = 1
	}
	var newHash *string
	if req.Password != "" {
		h2, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err == nil {
			s := string(h2)
			newHash = &s
		}
	}
	d := &store.UserDAO{}
	if err := d.Update(id, req.DisplayName, req.Role, req.Email, status, newHash); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	// 可选：更新 scope（数据范围）
	if bodyScope := getBodyField(body, "scope"); bodyScope != "" {
		_ = d.UpdateScope(id, req.Scope)
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

// getBodyField 从原始 body 提取指定字段（用于区分未传与空值）。
func getBodyField(body []byte, field string) string {
	var m map[string]interface{}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	v, ok := m[field].(string)
	if !ok {
		return ""
	}
	return v
}

// UserDelete DELETE /api/v1/users/{id} — 删除用户（admin）。
func (h *Handler) UserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "method not allowed", 405)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "bad id"})
		return
	}
	if err := (&store.UserDAO{}).Delete(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"ok": true})
}

// UserRouter 分发 /api/v1/users 下的 CRUD（admin 包装）。
func (h *Handler) UserRouter(w http.ResponseWriter, r *http.Request) {
	base := "/api/v1/users"
	idStr := strings.TrimPrefix(r.URL.Path, base+"/")
	if idStr == r.URL.Path {
		idStr = ""
	}
	if idStr == "" {
		// 集合操作：GET 列表 / POST 创建
		switch r.Method {
		case http.MethodGet:
			h.UserList(w, r)
		case http.MethodPost:
			h.UserCreate(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.UserUpdate(w, r)
	case http.MethodDelete:
		h.UserDelete(w, r)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// Me GET /api/v1/me — 当前登录用户信息。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	username, role, scopeClaim, ok := validateJWT(token)
	if !ok {
		respondJSON(w, 401, map[string]interface{}{"error": "unauthorized"})
		return
	}
	u, _ := (&store.UserDAO{}).GetByUsername(username)
	if u != nil {
		respondJSON(w, 200, map[string]interface{}{
			"username": u.Username, "role": u.Role, "display_name": u.DisplayName, "email": u.Email, "scope": u.Scope,
		})
		return
	}
	// 降级路径或用户不存在时返回 token 内信息
	respondJSON(w, 200, map[string]interface{}{"username": username, "role": role, "scope": scopeClaim})
}
