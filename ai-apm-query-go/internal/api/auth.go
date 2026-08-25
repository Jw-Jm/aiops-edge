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

	runInvocationIssuerMu sync.RWMutex
	runInvocationIssuer   *trustedauth.RunInvocationIssuer
)

// ConfigureRunInvocationIssuer installs the query-api → orchestrator RunInvocation
// issuer. A missing issuer leaves ProxyAI fail-closed (no unsigned privileged call).
func ConfigureRunInvocationIssuer(issuer *trustedauth.RunInvocationIssuer) {
	runInvocationIssuerMu.Lock()
	defer runInvocationIssuerMu.Unlock()
	runInvocationIssuer = issuer
}

func currentRunInvocationIssuer() *trustedauth.RunInvocationIssuer {
	runInvocationIssuerMu.RLock()
	defer runInvocationIssuerMu.RUnlock()
	return runInvocationIssuer
}

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
	// P3.9-A: query-api accepts only the V9.2 TrustedRequestContext (EdDSA, typ=AIOPS-CONTEXT).
	// Legacy single RequestContext (typ=JWT) is rejected by this verifier.
	context, err := trustedauth.VerifyTrustedRequestContextV2(r.Header.Get("X-Trusted-Request-Context"), *configured, time.Now())
	if err != nil {
		return AuthorizationContext{}, authorizationFailure("permission_denied")
	}
	// Internal service call: validate the principal's session is live, but do not
	// enforce JWT token_version (the internal context carries no JWT claim).
	return resolveMySQLAuthorizationContext(context.PrincipalID, context.SessionID, context.TenantID, -1)
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
	// Test/legacy helpers sign with token_version 0; the real login flow
	// (generateJWTWithSessionExpiry) reads the authoritative version from MySQL.
	return signJWT(userID, sessionID, time.Now().Add(24*time.Hour), 0)
}

func generateJWTWithSessionExpiry(userID, sessionID string, expiresAt time.Time) string {
	if userID == "" || sessionID == "" {
		return ""
	}
	// token_version is an invalidation mechanism, not an authorization fact (V9.2 §8).
	// Single authoritative source: user_sessions.token_version.
	tokenVersion := currentSessionTokenVersion(sessionID)
	return signJWT(userID, sessionID, expiresAt, tokenVersion)
}

// signJWT signs a token with an explicit token_version. Used by login (reads the
// authoritative version) and by tests (fixed version) without triggering a DB query.
func signJWT(userID, sessionID string, expiresAt time.Time, tokenVersion int64) string {
	if userID == "" || sessionID == "" {
		return ""
	}
	claims := jwt.MapClaims{
		"sub":           userID,
		"sid":           sessionID,
		"iat":           time.Now().Unix(),
		"exp":           expiresAt.Unix(),
		"token_version": tokenVersion,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	secret, err := getJWTSecret()
	if err != nil {
		return ""
	}
	signed, _ := t.SignedString(secret)
	return signed
}

// currentSessionTokenVersion reads the authoritative token_version for a session.
// DB unavailable returns 0; such a token cannot pass MySQL real-time authorization
// because the session lookup itself will fail.
func currentSessionTokenVersion(sessionID string) int64 {
	conn := store.GetDB()
	if conn == nil {
		return 0
	}
	var version int64
	if err := conn.QueryRow("SELECT token_version FROM user_sessions WHERE session_id = ?", sessionID).Scan(&version); err != nil {
		return 0
	}
	return version
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
	userID, sessionID, _, ok := validateJWTIdentityFull(tokenStr)
	return userID, sessionID, ok
}

// validateJWTIdentityFull returns userID, sessionID, token_version, ok.
func validateJWTIdentityFull(tokenStr string) (string, string, int64, bool) {
	secret, serr := getJWTSecret()
	if serr != nil {
		return "", "", 0, false
	}
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	})
	if err != nil || !t.Valid {
		return "", "", 0, false
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", 0, false
	}
	userID, _ := claims["sub"].(string)
	sessionID, _ := claims["sid"].(string)
	if userID == "" || sessionID == "" {
		return "", "", 0, false
	}
	var tokenVersion int64
	if raw, ok := claims["token_version"].(float64); ok {
		tokenVersion = int64(raw)
	}
	return userID, sessionID, tokenVersion, true
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
	userID, sessionID, tokenVersion, ok := validateJWTIdentityFull(token)
	if !ok || isTokenRevoked(token, userID) {
		return zero, authorizationFailure("permission_denied")
	}
	tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	if tenantID == "" || tenantID == "all" || !canonicalUUID.MatchString(tenantID) {
		return zero, authorizationFailure("invalid_context")
	}
	return resolveMySQLAuthorizationContext(userID, sessionID, tenantID, tokenVersion)
}

func resolveMySQLAuthorizationContext(userID, sessionID, tenantID string, tokenVersion int64) (AuthorizationContext, error) {
	var zero AuthorizationContext
	conn := store.GetDB()
	if conn == nil {
		return zero, authorizationFailure("cluster_unavailable")
	}
	var currentUserID, sessionStatus string
	var userStatus int
	var storedVersion int64
	var expiresAt, revokedAt sql.NullTime
	err := conn.QueryRow(`SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN user_sessions s ON s.user_uuid = u.user_uuid
WHERE u.user_uuid = ? AND s.session_id = ? LIMIT 1`, userID, sessionID).Scan(&currentUserID, &userStatus, &sessionStatus, &expiresAt, &revokedAt, &storedVersion)
	if err != nil || currentUserID != userID || userStatus != 1 || sessionStatus != "active" || !expiresAt.Valid || !expiresAt.Time.After(time.Now()) || (revokedAt.Valid && !revokedAt.Time.IsZero()) {
		return zero, authorizationFailure("permission_denied")
	}
	// token_version mismatch → session invalidated (V9.2 §8). Not an authorization fact.
	// tokenVersion == -1 skips JWT token_version enforcement for internal service calls.
	if tokenVersion != -1 && storedVersion != tokenVersion {
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

	// 1. MySQL identity/session authority: authenticate the password, require a
	// canonical user UUID, persist an active session, then sign only that UUID
	// and session ID. Role and scope remain response compatibility data, not JWT
	// authority.
	if db := store.GetDB(); db != nil {
		u, err := (&store.UserDAO{}).GetByUsername(creds.Username)
		if err == nil && u != nil && u.Status == 1 {
			if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(creds.Password)) == nil {
				if !canonicalUUID.MatchString(u.UserUUID) {
					respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
					return
				}
				sessionID := randomSessionID()
				expiresAt := time.Now().UTC().Add(24 * time.Hour)
				if sessionID == "" || (&store.UserDAO{}).CreateSession(u.UserUUID, sessionID, expiresAt) != nil {
					respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
					return
				}
				token := generateJWTWithSessionExpiry(u.UserUUID, sessionID, expiresAt)
				if token == "" {
					respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "auth backend unavailable"})
					return
				}
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

// hasRole 从 MySQL 权威角色 SoT 判定用户是否具备指定角色（admin 等）。
// 身份来源是 AuthMiddleware/RequestAuthorizationContext 已验签的 AuthorizationContext
// （userID 来自 JWT sub，但角色从 users.role 权威表读取），绝不信任 JWT role claim 或
// 客户端传入的 role —— 保持"JWT role claims are never authority"的反伪造性质。
// DB 不可达 / 用户不存在 / 非 active 一律 fail-closed 返回 false。
func hasRole(r *http.Request, role string) bool {
	authCtx, ok := requestAuthorizationContext(r)
	if !ok || authCtx.UserID == "" {
		return false
	}
	if db := store.GetDB(); db == nil {
		return false
	}
	u, err := (&store.UserDAO{}).GetByUUID(authCtx.UserID)
	if err != nil || u == nil || u.Status != 1 {
		return false
	}
	return u.Role == role
}

// RequireRole 返回按角色拦截的处理器包装（admin 仅限 admin 角色）。
// 角色来自 MySQL 权威 SoT（hasRole），JWT role claim 永不作为权限来源。
func (h *Handler) RequireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hasRole(r, role) {
			respondJSON(w, 403, map[string]interface{}{"error": "forbidden"})
			return
		}
		next(w, r)
	}
}

// RequireRoleForWrite 要求：HTTP 方法为非 GET/HEAD（写操作）且用户具备指定角色（admin）。
// 角色来自 MySQL 权威 SoT（hasRole）。仍保持 fail-closed：写操作在未迁移 canonical
// authorization 前，仅限具权威 admin 角色的用户；非写方法（GET）放行交给只读 handler。
func (h *Handler) RequireRoleForWrite(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// GET/HEAD 是读操作：交给 next（只读 handler 自行决定；写操作才要求 admin 角色）
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next(w, r)
			return
		}
		if !hasRole(r, role) {
			respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
			return
		}
		next(w, r)
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
	// canonical-protected：需 canonical tenant + JWT + user 是 tenant 成员（AuthMiddleware 已校验）
	// 才放行。P12 AUTH 修复：把前端实际使用的只读查询端点纳入 canonical 校验，消除
	// legacy 端点一律 403 permission_denied 导致的 AUTH BLOCKER。
	// 注意：仅只读 GET 查询端点；写端点（topology/alerts create、sync-catalog 等）不在此放行，
	// 保持 fail-closed。
	switch path {
	case "/api/v1/resources/resolve":
		return true
	case "/api/v1/services",
		"/api/v1/clusters", // 只读集群列表：前端集群选择器数据源（JWT+canonical tenant+成员）
		"/api/v1/traces",
		"/api/v1/topology/global",
		"/api/v1/topology/nodes",
		"/api/v1/topology/relations",
		"/api/v1/topology/node-types",
		"/api/v1/topology/relation-types",
		"/api/v1/alerts/rules",
		"/api/v1/alerts/events",
		"/api/v1/logs/query",
		"/api/v1/logs/aggregate",
		"/api/v1/dashboard/stats",
		"/api/v1/dashboard/resources",
		"/api/v1/capacity/forecast",
		"/api/v1/capacity/instances",
		"/api/v1/ai/runs",                // P12：Run API 只读代理（JWT+tenant 校验后进 ProxyAI → orchestrator）
		"/api/v1/me",                     // Phase E：前端用户信息端点（JWT + canonical tenant 授权）
		"/api/v1/settings/llm",           // LLM 设置：GET 读当前配置 / POST 保存（admin 由 RequireRoleForWrite 校验）
		"/api/v1/settings/llm/test",      // LLM 连接测试（admin 由 RequireRole 校验）
		"/api/v1/settings/llm/models",    // 拉取模型列表（admin 由 RequireRole 校验）
		"/api/v1/settings/llm/history",   // LLM 配置历史
		"/api/v1/settings/llm/providers", // LLM provider 列表/创建
		"/api/v1/ai/chat":                // P19.6：对话型 canonical-protected 路由。query-api 完成 JWT+tenant+cluster
		// 解析 + ai.chat capability 签名后转发 orchestrator /internal/v1/chat（SSE 流式）。
		// 不是公开放行：仍要求 JWT + canonical tenant + user 是 tenant 成员。
		return true
	}
	// LLM provider 子路径（/settings/llm/providers/{id} PUT/DELETE、/{id}/enable）：
	// canonical-protected（JWT+canonical tenant+成员），admin 角色由 handler RequireRole 校验。
	if strings.HasPrefix(path, "/api/v1/settings/llm/providers/") {
		return true
	}
	// LLM 配置历史子路径（/settings/llm/history/{version}/rollback）：
	// canonical-protected，admin 角色由 handler RequireRole 校验。
	if strings.HasPrefix(path, "/api/v1/settings/llm/history/") {
		return true
	}
	// P10 (V9.3 Phase 10)：公共 SSE proxy / Control——GET .../events（JWT+tenant 授权）、
	// POST .../cancel（显式 control action）。均需 canonical 鉴权。
	if strings.HasPrefix(path, "/api/v1/ai/runs/") &&
		(strings.HasSuffix(path, "/events") || strings.HasSuffix(path, "/cancel")) {
		return true
	}
	// A0-04（11.11.7）：/api/v1/ai/runs/{runID} 单段详情——GetRunPublic 已有
	// tenant/run ownership 校验（与 SSE/cancel 同一 canonical 校验），放行不扩大 scope。
	if strings.HasPrefix(path, "/api/v1/ai/runs/") &&
		!strings.HasSuffix(path, "/") &&
		strings.Count(strings.TrimPrefix(path, "/api/v1/ai/runs/"), "/") == 0 {
		return true
	}
	// C2-4：/api/v1/ai/runs/{runID}/tools——真实 ToolRun 只读展示（GetRunToolsPublic 有
	// tenant/run ownership 校验），不推断冒充。
	if strings.HasPrefix(path, "/api/v1/ai/runs/") && strings.HasSuffix(path, "/tools") {
		return true
	}
	// Stage D 接线：/api/v1/ai/actions/{id}(/execute)——action 详情/执行端点。
	// canonical 鉴权（JWT + tenant 成员）由 AuthMiddleware 完成；POST execute 需 admin
	// （RequireRoleForWrite 在 main.go 注册时套用）。handler 内部再做 action tenant 归属校验。
	if strings.HasPrefix(path, "/api/v1/ai/actions") {
		return true
	}
	// A0-04（11.11.4）：/api/v1/metrics/query——typed RED metrics（带 service + concrete
	// canonical cluster），QueryMetrics 已改为 canonical tenant + concrete cluster fail-closed；
	// 任意 PromQL 直通已关闭（无 service → 400），不构成任意 PromQL 透传面。
	if path == "/api/v1/metrics/query" {
		return true
	}
	return false
}

// AuthMiddleware 鉴权中间件：公开端点放行；内部服务（X-Internal-Token）放行；其余必须 JWT。
// G1 安全加固：JWT 校验通过后，进一步校验 token 未被撤销、用户仍存在且 status==1
// （删除/禁用用户后其 token 立即失效，不再依赖 24h 过期）。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public endpoints: health, login, OPTIONS. LLM settings, including
		// the non-secret status endpoint, require an authorization context.
		if path == "/health" || path == "/livez" || path == "/readyz" || path == "/api/v1/health" ||
			path == "/metrics" || // 自监控：query-api 自身 Prometheus 指标，供 VM 免鉴权抓取
			path == "/api/v1/auth/login" || path == "/api/v1/login" ||
			r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// P6.2e / P6.5 A: /internal/v1/* 是 internal service boundary，由各
		// InternalQuery* handler 自行执行 TrustedRequestContext V2 鉴权
		// （authorizeInternalQuery：X-Internal-Token + 签名 context + capability +
		// scope match，无 JWT fallback）。公共 AuthMiddleware 在此放行，不重复
		// 执行 JWT/tenant 校验；此放行不改动 /api/v1/* 公共 API 安全边界。
		if strings.HasPrefix(path, "/internal/v1/") {
			next.ServeHTTP(w, r)
			return
		}

		// P19.6: /api/v1/settings/llm/internal 是 orchestrator 拉取真实 LLM 配置
		// （含解密 API Key）的内部端点，由 GetInternalLLMSettings 自行用 X-Internal-Token
		// 鉴权（无 JWT fallback），与 /internal/v1/* 同 internal-boundary 模式。
		// 公共 AuthMiddleware 在此放行，避免 orchestrator 拿不到 LLM key（真实环境 AUTH 接线缺陷）。
		if path == "/api/v1/settings/llm/internal" {
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
