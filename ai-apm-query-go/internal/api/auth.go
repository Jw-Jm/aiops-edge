package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// AuthorizationContext is the request identity and tenant membership verified
// from the current MySQL state. It intentionally contains no JWT role or scope.
type AuthorizationContext struct {
	UserID    string
	SessionID string
	TenantID  string
}

type authorizationError struct{ code string }

func (err *authorizationError) Error() string { return err.code }

func isAuthorizationError(err error, code string) bool {
	return err != nil && err.Error() == code
}

func authorizationFailure(code string) error { return &authorizationError{code: code} }

type authorizationContextKey struct{}

var (
	internalVerifierMu sync.RWMutex
	internalVerifier   *trustedauth.VerifyConfig
)

// configureInternalRequestVerifier installs the independent service-token and
// signed-context verifier. It is kept small so production wiring and tests use
// exactly the same verification path.
func configureInternalRequestVerifier(cfg trustedauth.VerifyConfig) func() {
	internalVerifierMu.Lock()
	previous := internalVerifier
	configured := cfg
	internalVerifier = &configured
	internalVerifierMu.Unlock()
	return func() {
		internalVerifierMu.Lock()
		internalVerifier = previous
		internalVerifierMu.Unlock()
	}
}

// ConfigureInternalRequestVerifier installs production service authentication
// and signed-context verification. A missing configuration is intentionally
// not replaced with a default: internal requests then fail closed.
func ConfigureInternalRequestVerifier(cfg trustedauth.VerifyConfig) {
	configureInternalRequestVerifier(cfg)
}

func internalRequestAuthorizationContext(r *http.Request) (AuthorizationContext, error) {
	internalVerifierMu.RLock()
	configured := internalVerifier
	internalVerifierMu.RUnlock()
	if configured == nil {
		return AuthorizationContext{}, authorizationFailure("permission_denied")
	}
	if err := trustedauth.VerifyServiceToken(r.Header.Get("X-Internal-Token"), *configured); err != nil {
		return AuthorizationContext{}, authorizationFailure("permission_denied")
	}
	context, err := trustedauth.VerifyTrustedRequestContext(r.Header.Get("X-Trusted-Request-Context"), *configured, time.Now())
	if err != nil {
		return AuthorizationContext{}, authorizationFailure("permission_denied")
	}
	return resolveMySQLAuthorizationContext(context.UserID, context.SessionID, context.TenantID)
}

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

// generateJWT is retained for legacy callers, but its role/scope arguments are
// deliberately ignored. JWTs now prove only identity and a session handle.
func generateJWT(username, role, scope string) string {
	return generateJWTWithSession(username, randomSessionID(), role, scope)
}

func generateJWTWithSession(userID, sessionID, _ string, _ string) string {
	if userID == "" || sessionID == "" {
		return ""
	}
	claims := jwt.MapClaims{
		"sub": userID,
		"sid": sessionID,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	secret, err := getJWTSecret()
	if err != nil {
		return ""
	}
	signed, _ := t.SignedString(secret)
	return signed
}

func randomSessionID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:])
}

// validateJWT validates the identity/session token. The returned role and
// scope slots remain empty solely for source compatibility with legacy callers.
func validateJWT(tokenStr string) (string, string, string, bool) {
	userID, _, ok := validateJWTIdentity(tokenStr)
	return userID, "", "", ok
}

func validateJWTIdentity(tokenStr string) (string, string, bool) {
	secret, serr := getJWTSecret()
	if serr != nil {
		return "", "", false
	}
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	})
	if err != nil || !t.Valid {
		return "", "", false
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", false
	}
	userID, _ := claims["sub"].(string)
	sessionID, _ := claims["sid"].(string)
	if userID == "" || sessionID == "" {
		return "", "", false
	}
	return userID, sessionID, true
}

// currentScope deliberately returns no caller-controlled scope. Authorization
// decisions for new protected routes are made from MySQL in
// RequestAuthorizationContext and AuthorizationDAO.
func currentScope(r *http.Request) *Scope {
	return &Scope{}
}

// RequestAuthorizationContext validates a JWT identity/session and checks the
// requested compatibility tenant hint against current MySQL identity, session,
// and membership rows. X-Tenant-ID never becomes trusted until these checks
// succeed, and is not copied to internal service headers.
func RequestAuthorizationContext(r *http.Request) (AuthorizationContext, error) {
	if r.Header.Get("X-Internal-Token") != "" || r.Header.Get("X-Trusted-Request-Context") != "" {
		return internalRequestAuthorizationContext(r)
	}
	var zero AuthorizationContext
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	userID, sessionID, ok := validateJWTIdentity(token)
	if !ok || isTokenRevoked(token, userID) {
		return zero, authorizationFailure("permission_denied")
	}
	tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	if tenantID == "" || tenantID == "all" || !canonicalUUID.MatchString(tenantID) {
		return zero, authorizationFailure("invalid_context")
	}
	return resolveMySQLAuthorizationContext(userID, sessionID, tenantID)
}

func resolveMySQLAuthorizationContext(userID, sessionID, tenantID string) (AuthorizationContext, error) {
	var zero AuthorizationContext
	conn := store.GetDB()
	if conn == nil {
		return zero, authorizationFailure("cluster_unavailable")
	}
	var currentUserID, sessionStatus string
	var userStatus int
	var expiresAt, revokedAt sql.NullTime
	err := conn.QueryRow(`SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at FROM users u JOIN user_sessions s ON s.user_uuid = u.user_uuid
WHERE u.user_uuid = ? AND s.session_id = ? LIMIT 1`, userID, sessionID).Scan(&currentUserID, &userStatus, &sessionStatus, &expiresAt, &revokedAt)
	if err != nil || currentUserID != userID || userStatus != 1 || sessionStatus != "active" || !expiresAt.Valid || !expiresAt.Time.After(time.Now()) || (revokedAt.Valid && !revokedAt.Time.IsZero()) {
		return zero, authorizationFailure("permission_denied")
	}
	var memberTenantID string
	err = conn.QueryRow(`SELECT t.id FROM tenants t JOIN user_tenants ut ON ut.tenant_id = t.id
WHERE ut.user_uuid = ? AND t.id = ? AND t.enabled = 1 AND ut.status = 'active' LIMIT 1`, userID, tenantID).Scan(&memberTenantID)
	if err != nil || memberTenantID != tenantID {
		return zero, authorizationFailure("permission_denied")
	}
	return AuthorizationContext{UserID: userID, SessionID: sessionID, TenantID: tenantID}, nil
}

func requestAuthorizationContext(r *http.Request) (AuthorizationContext, bool) {
	context, ok := r.Context().Value(authorizationContextKey{}).(AuthorizationContext)
	return context, ok
}

func withAuthorizationContext(r *http.Request, authorization AuthorizationContext) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authorizationContextKey{}, authorization))
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

// hasRole deliberately fails closed while legacy handlers have no canonical
// AuthorizationDAO action/scope mapping. JWT role claims are never authority.
func hasRole(r *http.Request, role string) bool {
	return false
}

// RequireRoleForWrite fences legacy routes until each operation is migrated to
// a canonical AuthorizationDAO action and resource scope. Neither read nor
// write methods may regain authority from a JWT claim.
func (h *Handler) RequireRoleForWrite(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
	}
}

// ── Token 撤销（G1 安全加固）──
// revokedTokens 按原始 JWT 字符串撤销（登出/单 token 失效）；
// revokedUsers 按用户名撤销（删除/禁用用户后其全部 token 立即失效）。
// 纯内存实现：进程重启后失效（与 JWT 24h 有效期相比可接受，生产可换 Redis）。
var (
	revokedTokens   = map[string]bool{}
	revokedUsers    = map[string]bool{}
	revokedTokensMu sync.Mutex
)

// revokeToken 撤销指定 JWT 字符串（登出场景）。
func revokeToken(token string) {
	if token == "" {
		return
	}
	revokedTokensMu.Lock()
	revokedTokens[token] = true
	revokedTokensMu.Unlock()
}

// revokeUser 撤销指定用户名的全部 token（删除/禁用用户后即时失效）。
func revokeUser(username string) {
	if username == "" {
		return
	}
	revokedTokensMu.Lock()
	revokedUsers[username] = true
	revokedTokensMu.Unlock()
}

// isTokenRevoked 判断 token 或其用户名是否已被撤销。
func isTokenRevoked(token, username string) bool {
	revokedTokensMu.Lock()
	defer revokedTokensMu.Unlock()
	if revokedTokens[token] {
		return true
	}
	return revokedUsers[username]
}

// hasPrivilegedRole deliberately fails closed until privileged legacy paths
// are mapped to canonical MySQL actions and resource scopes.
func hasPrivilegedRole(r *http.Request) bool {
	return false
}

func isCanonicalProtectedRoute(path string) bool {
	return path == "/api/v1/resources/resolve"
}

// AuthMiddleware 鉴权中间件：公开端点放行；内部服务（X-Internal-Token）放行；其余必须 JWT。
// G1 安全加固：JWT 校验通过后，进一步校验 token 未被撤销、用户仍存在且 status==1
// （删除/禁用用户后其 token 立即失效，不再依赖 24h 过期）。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public endpoints: health, login, OPTIONS. LLM settings, including
		// the non-secret status endpoint, require an authorization context.
		if path == "/health" || path == "/api/v1/health" ||
			path == "/metrics" || // 自监控：query-api 自身 Prometheus 指标，供 VM 免鉴权抓取
			path == "/api/v1/auth/login" || path == "/api/v1/login" ||
			r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// Browser and internal service requests resolve current MySQL identity,
		// session, and requested tenant membership. Internal callers must also
		// present a signed context; JWT role/scope never grants authority.
		authorization, err := RequestAuthorizationContext(r)
		if err != nil {
			status := http.StatusForbidden
			if isAuthorizationError(err, "invalid_context") {
				status = http.StatusBadRequest
			} else if isAuthorizationError(err, "cluster_unavailable") {
				status = http.StatusServiceUnavailable
			}
			respondJSON(w, status, map[string]interface{}{"error": err.Error()})
			return
		}
		if !isCanonicalProtectedRoute(path) {
			respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
			return
		}
		next.ServeHTTP(w, withAuthorizationContext(r, authorization))
	})
}
