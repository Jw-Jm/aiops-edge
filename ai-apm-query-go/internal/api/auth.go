package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

var jwtSecret = func() []byte {
	if s := os.Getenv("JWT_SECRET"); s != "" {
		return []byte(s)
	}
	return []byte("observability-platform-secret")
}()

// generateJWT 生成标准 HS256 JWT，携带 sub(username) 与 role。
func generateJWT(username, role string) string {
	claims := jwt.MapClaims{
		"sub":  username,
		"role": role,
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := t.SignedString(jwtSecret)
	return signed
}

// validateJWT 校验 JWT，返回 username、role 与是否有效。
func validateJWT(tokenStr string) (string, string, bool) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jwtSecret, nil
	})
	if err != nil || !t.Valid {
		return "", "", false
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", false
	}
	username, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	return username, role, true
}

// Login 登录：查 MySQL users 表 + bcrypt 校验；MySQL 不可达降级 admin/admin123。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var creds struct {
		Username, Password string
	}
	json.Unmarshal(body, &creds)

	// 1. MySQL 优先：查用户 + bcrypt 校验
	if db := store.GetDB(); db != nil {
		u, err := (&store.UserDAO{}).GetByUsername(creds.Username)
		if err == nil && u != nil && u.Status == 1 {
			if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(creds.Password)) == nil {
				token := generateJWT(u.Username, u.Role)
				respondJSON(w, 200, map[string]interface{}{
					"token": token, "username": u.Username, "role": u.Role, "display_name": u.DisplayName,
				})
				return
			}
		}
	}

	// 2. MySQL 不可达降级：内置 admin/admin123（仅数据库故障路径）
	if store.GetDB() == nil && creds.Username == "admin" && creds.Password == "admin123" {
		respondJSON(w, 200, map[string]interface{}{
			"token": generateJWT("admin", "admin"), "username": "admin", "role": "admin", "degraded": true,
		})
		return
	}

	respondJSON(w, 401, map[string]interface{}{"error": "invalid credentials"})
}

// isInternalRequest 仅信任带 X-Internal-Token 的服务间调用（已移除 IP 白名单免鉴权）。
func isInternalRequest(r *http.Request) bool {
	return r.Header.Get("X-Internal-Token") == os.Getenv("INTERNAL_TOKEN")
}

// RequireRole 返回按角色拦截的处理器包装（admin 仅限 admin 角色）。
func (h *Handler) RequireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, gotRole, ok := validateJWT(token)
		if !ok {
			respondJSON(w, 401, map[string]interface{}{"error": "unauthorized"})
			return
		}
		if gotRole != role {
			respondJSON(w, 403, map[string]interface{}{"error": "forbidden"})
			return
		}
		next(w, r)
	}
}

// AuthMiddleware 鉴权中间件：公开端点放行；内部服务（X-Internal-Token）放行；其余必须 JWT。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public endpoints: health, login, OPTIONS, settings setup
		if path == "/health" || path == "/api/v1/health" ||
			path == "/api/v1/auth/login" || path == "/api/v1/login" ||
			strings.HasPrefix(path, "/api/v1/settings/llm") ||
			r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// 内部服务间调用（X-Internal-Token）
		if isInternalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		// 其余所有请求必须带合法 JWT
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, _, ok := validateJWT(token); !ok {
			respondJSON(w, 401, map[string]interface{}{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
