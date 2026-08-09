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

// Scope 数据范围（三维：services/clusters/devices）。空=全量（admin）。
type Scope struct {
	Services []string `json:"services"`
	Clusters []string `json:"clusters"`
	Devices  []string `json:"devices"`
}

// IsFull 空 scope（或 nil）= 不限制 = 全量。
func (s *Scope) IsFull() bool {
	return s == nil || (len(s.Services) == 0 && len(s.Clusters) == 0 && len(s.Devices) == 0)
}

func (s *Scope) ContainsService(name string) bool {
	if s == nil || len(s.Services) == 0 {
		return true // 该维度未限定 => 全通过
	}
	for _, x := range s.Services {
		if x == name {
			return true
		}
	}
	return false
}

func (s *Scope) ContainsCluster(name string) bool {
	if s == nil || len(s.Clusters) == 0 {
		return true // 该维度未限定 => 全通过
	}
	for _, x := range s.Clusters {
		if x == name {
			return true
		}
	}
	return false
}

func (s *Scope) ContainsDevice(name string) bool {
	if s == nil || len(s.Devices) == 0 {
		return true // 该维度未限定 => 全通过
	}
	for _, x := range s.Devices {
		if x == name {
			return true
		}
	}
	return false
}

// parseScope 解析 scope JSON 字符串。
func parseScope(raw string) *Scope {
	sc := &Scope{}
	if raw == "" {
		return sc
	}
	_ = json.Unmarshal([]byte(raw), sc)
	return sc
}

// generateJWT 生成标准 HS256 JWT，携带 sub(username)、role 与 scope。
func generateJWT(username, role, scope string) string {
	claims := jwt.MapClaims{
		"sub":   username,
		"role":  role,
		"scope": scope,
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := t.SignedString(jwtSecret)
	return signed
}

// validateJWT 校验 JWT，返回 username、role、scope 与是否有效。
func validateJWT(tokenStr string) (string, string, string, bool) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jwtSecret, nil
	})
	if err != nil || !t.Valid {
		return "", "", "", false
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", "", false
	}
	username, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	scope, _ := claims["scope"].(string)
	return username, role, scope, true
}

// currentScope 从请求提取当前用户的数据范围。
func currentScope(r *http.Request) *Scope {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, _, scope, ok := validateJWT(token)
	if !ok {
		return &Scope{}
	}
	return parseScope(scope)
}

// Login 登录：查 MySQL users 表 + bcrypt 校验；MySQL 不可达返回 503（禁止任何弱口令降级放行）。
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
				token := generateJWT(u.Username, u.Role, u.Scope)
				respondJSON(w, 200, map[string]interface{}{
					"token": token, "username": u.Username, "role": u.Role, "display_name": u.DisplayName, "scope": u.Scope,
				})
				return
			}
		}
	}

	// 2. 认证后端不可达：返回 503，禁止任何降级放行（安全：DB 故障不允许用内置弱口令登录）
	if store.GetDB() == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
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
		_, gotRole, _, ok := validateJWT(token)
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
		if _, _, _, ok := validateJWT(token); !ok {
			respondJSON(w, 401, map[string]interface{}{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
