package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// jwtSecret 签名密钥。必须通过 JWT_SECRET 环境变量显式注入（生产从 Secret/KMS 注入）。
// 缺失时 panic，绝不使用内置弱密钥（否则任何人都能伪造 admin token）。
// 用 sync.Once 惰性初始化，便于测试注入（TestMain 设置 env 后首次调用才求值）。
var (
	jwtSecretOnce sync.Once
	jwtSecret     []byte
	jwtSecretErr  error
)

func getJWTSecret() ([]byte, error) {
	jwtSecretOnce.Do(func() {
		s := os.Getenv("JWT_SECRET")
		if len(s) < 32 {
			jwtSecretErr = fmt.Errorf("JWT_SECRET must be set and at least 32 chars long (generate e.g. 'openssl rand -hex 32')")
			return
		}
		jwtSecret = []byte(s)
	})
	return jwtSecret, jwtSecretErr
}

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
	secret, err := getJWTSecret()
	if err != nil {
		return ""
	}
	signed, _ := t.SignedString(secret)
	return signed
}

// validateJWT 校验 JWT，返回 username、role、scope 与是否有效。
func validateJWT(tokenStr string) (string, string, string, bool) {
	secret, serr := getJWTSecret()
	if serr != nil {
		return "", "", "", false
	}
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
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

// ── 登录暴力破解防护（安全 P1-1）──
// loginAttempts 按 username+IP 维度记录登录尝试次数：60s 窗口内最多 10 次，
// 超限返回 429。纯内存实现，不引入外部依赖。
var (
	loginAttempts   = map[string]loginAttempt{}
	loginAttemptsMu sync.Mutex
)

type loginAttempt struct {
	count  int64
	window int64 // unix 秒（窗口起点）
}

const (
	loginAttemptLimit  = 10
	loginAttemptWindow = 60 // 秒
)

// clientIP 提取客户端 IP（透传 X-Forwarded-For 首个 IP；未设置用 RemoteAddr）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allowLoginAttempt 记录一次登录尝试并判断是否超限（超限返回 false）。
// 窗口过期即重置计数；map 超阈值时惰性清理过期条目，防无限增长。
func allowLoginAttempt(key string) bool {
	now := time.Now().Unix()
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	if len(loginAttempts) > 10000 {
		for k, a := range loginAttempts {
			if now-a.window >= loginAttemptWindow {
				delete(loginAttempts, k)
			}
		}
	}
	a, ok := loginAttempts[key]
	if !ok || now-a.window >= loginAttemptWindow {
		a = loginAttempt{count: 0, window: now}
	}
	a.count++
	loginAttempts[key] = a
	return a.count <= loginAttemptLimit
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

	// 暴力破解防护（安全 P1-1）：按 username+IP 每 60s 最多 10 次尝试，超限 429
	if !allowLoginAttempt(creds.Username + "|" + clientIP(r)) {
		respondJSON(w, 429, map[string]interface{}{"error": "尝试过于频繁, 请稍后再试"})
		return
	}

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
// 安全：只有当 INTERNAL_TOKEN 已显式配置时才启用内部放行分支；未配置时一律返回 false，
// 避免"空 token == 空 header"的鉴权绕过。
func isInternalRequest(r *http.Request) bool {
	internalToken := os.Getenv("INTERNAL_TOKEN")
	if internalToken == "" {
		return false
	}
	got := r.Header.Get("X-Internal-Token")
	if got == "" {
		return false
	}
	return got == internalToken
}

// RequireRole 返回按角色拦截的处理器包装（admin 仅限 admin 角色）。
func (h *Handler) RequireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hasRole(r, role) {
			respondJSON(w, 403, map[string]interface{}{"error": "forbidden"})
			return
		}
		next(w, r)
	}
}

// hasRole 判断请求携带的 JWT 是否具有指定角色（供 handler 内做细粒度权限校验）。
func hasRole(r *http.Request, role string) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, gotRole, _, ok := validateJWT(token)
	if !ok {
		return false
	}
	return gotRole == role
}

// RequireRoleForWrite 仅在写方法（POST/PUT/PATCH/DELETE）上要求指定角色，
// 读方法（GET/HEAD/OPTIONS）放行。用于"读开放、写需 admin"的资源路由
// （服务目录/设备/集群），避免任意登录用户越权写。
func (h *Handler) RequireRoleForWrite(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(w, r)
			return
		}
		if !hasRole(r, role) {
			respondJSON(w, 403, map[string]interface{}{"error": "forbidden: requires admin"})
			return
		}
		next(w, r)
	}
}

// AuthMiddleware 鉴权中间件：公开端点放行；内部服务（X-Internal-Token）放行；其余必须 JWT。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public endpoints: health, login, OPTIONS
		// 仅放行"读取 LLM 配置状态"的 GET /api/v1/settings/llm（返回的是加密后的配置，
		// 不含明文 key，用于前端判断是否已配置）。所有写操作（POST/PUT 保存）、
		// 以及敏感子路径（/internal、/providers、/test、/models、/history、rollback）
		// 一律走 isInternalRequest 或 JWT 鉴权，防止未授权写入 LLM API key。
		if path == "/health" || path == "/api/v1/health" ||
			path == "/metrics" || // 自监控：query-api 自身 Prometheus 指标，供 VM 免鉴权抓取
			path == "/api/v1/auth/login" || path == "/api/v1/login" ||
			(path == "/api/v1/settings/llm" && r.Method == http.MethodGet) ||
			// WebShell WebSocket：无自定义 header，token 经 ?token= query 传递，由 ProxyShellWS
			// 内部验证 JWT（不能在此处拦截，否则 WebSocket 升级请求会被 401 拒绝）。
			path == "/api/v1/shell/ws" ||
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
