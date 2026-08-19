package api

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// llmEncryptionKey 用于对 LLM API key 做 AES-256-GCM 加解密。必须通过
// LLM_ENCRYPTION_KEY 环境变量显式注入（生产从 Secret 注入，至少 32 字节）。
// 缺失时绝不退化为"明文存储/返回"。用 sync.Once 惰性求值，便于测试注入。
var (
	llmEncryptionKeyOnce sync.Once
	llmEncryptionKey     []byte
	llmEncryptionKeyErr  error
)

func getLLMEncryptionKey() ([]byte, error) {
	llmEncryptionKeyOnce.Do(func() {
		k := os.Getenv("LLM_ENCRYPTION_KEY")
		if len(k) < 32 {
			llmEncryptionKeyErr = fmt.Errorf("LLM_ENCRYPTION_KEY must be set and at least 32 bytes long (generate e.g. 'openssl rand -hex 32')")
			return
		}
		// Pad or truncate to 32 bytes for AES-256
		b := []byte(k)
		key := make([]byte, 32)
		copy(key, b)
		llmEncryptionKey = key
	})
	return llmEncryptionKey, llmEncryptionKeyErr
}

// encryptAPIKey 用 AES-256-GCM 加密 API key。key 缺失时返回空串（由调用方拒绝保存），
// 绝不返回明文。
func encryptAPIKey(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	key, err := getLLMEncryptionKey()
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return ""
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

// decryptAPIKey 解密 API key。key 缺失或解密失败时返回空串，绝不返回密文/明文。
func decryptAPIKey(encoded string) string {
	if encoded == "" {
		return ""
	}
	key, kerr := getLLMEncryptionKey()
	if kerr != nil {
		return ""
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return ""
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ""
	}
	return string(plaintext)
}

// Settings represents the application settings stored on disk.
type Settings struct {
	LLM LLMSettings `json:"llm"`
	K8s K8sSettings `json:"k8s"`
}

// LLMSettings holds the LLM provider configuration.
type LLMSettings struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
}

// K8sSettings holds K8s-related configuration.
type K8sSettings struct {
	Enabled  bool   `json:"enabled"`
	AuthType string `json:"auth_type"`
}

var (
	settings   *Settings
	settingsMu sync.RWMutex
)

func init() {
	settings = loadSettings()
}

func loadSettings() *Settings {
	s := &Settings{
		LLM: LLMSettings{Provider: "openai", Model: "gpt-4o", BaseURL: "https://api.openai.com/v1"},
		K8s: K8sSettings{Enabled: true, AuthType: "ServiceAccount"},
	}
	if data, err := loadFromStore("llm_settings"); err == nil && data != "" {
		json.Unmarshal([]byte(data), &s.LLM)
	}
	if data, err := loadFromStore("k8s_settings"); err == nil && data != "" {
		json.Unmarshal([]byte(data), &s.K8s)
	}
	return s
}

func saveSettings(s *Settings) error {
	llmData, _ := json.Marshal(s.LLM)
	saveToStore("llm_settings", string(llmData))
	k8sData, _ := json.Marshal(s.K8s)
	saveToStore("k8s_settings", string(k8sData))
	return nil
}

// loadFromStore 从 MySQL platform_settings 读配置项（配置类从 CH 迁 MySQL）。
func loadFromStore(key string) (string, error) {
	d := &store.SettingDAO{}
	v, err := d.Get(key)
	if err != nil || v == "" {
		return "", fmt.Errorf("not found: %v", err)
	}
	return v, nil
}

func saveToStore(key, value string) error {
	d := &store.SettingDAO{}
	return d.Set(key, value)
}

// ═══════════════════════════════════════════════════════
//  LLM Config History (multi-provider versioning)
// ═══════════════════════════════════════════════════════

type LLMConfigHistory struct {
	Version   int    `json:"version"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url"`
	CreatedAt string `json:"created_at"`
	Operator  string `json:"operator"`
	Comment   string `json:"comment"`
	IsCurrent bool   `json:"is_current"`
}

// SaveLLMHistory saves current config to history table and increments version.
func saveLLMHistory(llm LLMSettings) {
	// Hash API Key (don't store raw)
	apiKeyHash := "****"
	if llm.APIKey != "" {
		h := sha256Hash(llm.APIKey)
		apiKeyHash = "sha256:" + h[:16]
	}
	d := &store.LLMConfigHistoryDAO{}
	_ = d.Append(llm.Provider, llm.Model, llm.BaseURL, apiKeyHash, "", "config save")
}

// ListLLMHistory handles GET /api/v1/settings/llm/history
func (h *Handler) ListLLMHistory(w http.ResponseWriter, r *http.Request) {
	// Get current config for comparison
	settingsMu.RLock()
	current := settings.LLM
	settingsMu.RUnlock()

	d := &store.LLMConfigHistoryDAO{}
	rows, err := d.List(20)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": "mysql unavailable"})
		return
	}
	history := []LLMConfigHistory{}
	for _, row := range rows {
		history = append(history, LLMConfigHistory{
			Version:   int(row.Version),
			Provider:  row.Provider,
			Model:     row.Model,
			BaseURL:   row.BaseURL,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05"),
			Operator:  row.Operator,
			Comment:   row.Comment,
			IsCurrent: row.Provider == current.Provider && row.Model == current.Model && row.BaseURL == current.BaseURL,
		})
	}
	respondJSON(w, 200, map[string]interface{}{"history": history, "total": len(history)})
}

// RollbackLLMConfig handles POST /api/v1/settings/llm/history/{version}/rollback
func (h *Handler) RollbackLLMConfig(w http.ResponseWriter, r *http.Request) {
	// Extract version from URL: /api/v1/settings/llm/history/{version}/rollback
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/history/"), "/")
	if len(parts) < 2 || parts[1] != "rollback" {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid path"})
		return
	}
	var ver int64
	fmt.Sscanf(parts[0], "%d", &ver)

	d := &store.LLMConfigHistoryDAO{}
	row, err := d.GetVersion(ver)
	if err != nil || row == nil {
		respondJSON(w, 404, map[string]interface{}{"error": "version not found"})
		return
	}

	// Restore provider/model/base_url, keep current API Key
	settingsMu.Lock()
	settings.LLM.Provider = row.Provider
	settings.LLM.Model = row.Model
	settings.LLM.BaseURL = row.BaseURL
	settingsMu.Unlock()

	saveSettings(settings)
	respondJSON(w, 200, map[string]interface{}{
		"message":  "rolled back",
		"provider": row.Provider,
		"model":    row.Model,
		"base_url": row.BaseURL,
	})
}

func sha256Hash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}
func (h *Handler) SettingsLLM(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetLLMSettings(w, r)
	case http.MethodPost:
		h.SaveLLMSettings(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
	}
}

// GetLLMSettings handles GET /api/v1/settings/llm
func (h *Handler) GetLLMSettings(w http.ResponseWriter, r *http.Request) {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	llm := settings.LLM
	// P0-1 修复: "已配置"必须是真实可用的 —— 仅 provider/model/base_url 字段非空不足以
	// 说明 LLM 可用, API key 必须能解密成功才算 api_key_set/configured。
	// 否则密钥漂移/解密失败时界面会误报"已配置", 而实际 AI 全部降级为确定性模式。
	decrypted := decryptAPIKey(llm.APIKey)
	apiKeySet := decrypted != ""
	configured := llm.Provider != "" && llm.Model != "" && llm.BaseURL != "" && apiKeySet
	// This compatibility status endpoint intentionally exposes only readiness.
	// It does not disclose provider/model topology, endpoint locations, or any
	// secret-derived field.
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{
			"configured": configured,
		},
	})
}

// GetInternalLLMSettings handles GET /api/v1/settings/llm/internal
// 仅供内部服务(ai-orchestrator)使用，返回解密后的真实 API Key。
// 通过 X-Internal-Token 鉴权。
func (h *Handler) GetInternalLLMSettings(w http.ResponseWriter, r *http.Request) {
	// 鉴权：X-Internal-Token 必须非空且匹配（INTERNAL_TOKEN 未配置时一律拒绝，
	// 避免"空 token == 空 header"绕过）
	internalToken := os.Getenv("INTERNAL_TOKEN")
	got := r.Header.Get("X-Internal-Token")
	if internalToken == "" || got == "" || got != internalToken {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	real := settings.LLM
	real.APIKey = decryptAPIKey(real.APIKey)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": real,
	})
}

// validateLLMBaseURL 校验 LLM base_url（安全 P0-4，防 SSRF / API key 窃取）：
// 必须 https；主机名不得为 localhost、含 "metadata" 的主机名、私网/回环/链路本地
// IP（含 10.x/192.168.x/172.16-31.x/127.x/169.254.x 云 metadata 169.254.169.254）。
// 域名解析结果含内网 IP 同样拒绝（防 DNS 别名指向内网）。返回空串=允许。
func validateLLMBaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "URL 无效"
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "必须使用 https"
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.Contains(host, "metadata") {
		return "不允许指向本地/内网/metadata 地址"
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return "不允许指向本地/内网/metadata 地址"
		}
		return ""
	}
	// 域名：解析结果含内网 IP 同样拒绝（防 DNS 别名指向内网）
	if addrs, err := net.LookupHost(host); err == nil {
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && isBlockedIP(ip) {
				return "不允许指向本地/内网/metadata 地址"
			}
		}
	}
	return ""
}

// isBlockedIP 判断 IP 是否属于禁止访问的私网/回环/链路本地地址。
// IPv6 ULA(fc00::/7) 放行——避免 OrbStack DNS 把 api.deepseek.com 解析为 ULA
// 时误伤合法公网域名。IPv4 用 RFC1918 手动判断（Go 的 IsPrivate 含 fc00::/7）。
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 || (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) || (ip4[0] == 192 && ip4[1] == 168)
	}
	return false
}

// SaveLLMSettings handles POST /api/v1/settings/llm
func (h *Handler) SaveLLMSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read body"})
		return
	}
	defer r.Body.Close()

	var llm LLMSettings
	if err := json.Unmarshal(body, &llm); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid JSON: " + err.Error()})
		return
	}

	settingsMu.Lock()
	if llm.Provider != "" {
		settings.LLM.Provider = llm.Provider
	}
	if llm.APIKey != "" {
		enc := encryptAPIKey(llm.APIKey)
		if enc == "" {
			settingsMu.Unlock()
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "encryption key unavailable, cannot store API key safely",
			})
			return
		}
		settings.LLM.APIKey = enc
	}
	if llm.Model != "" {
		settings.LLM.Model = llm.Model
	}
	if llm.BaseURL != "" {
		// 安全(P0-4)：base_url 必须 https 且非私网/metadata 地址，防止 SSRF 窃取已保存 key
		if msg := validateLLMBaseURL(llm.BaseURL); msg != "" {
			settingsMu.Unlock()
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "base_url 校验失败: " + msg})
			return
		}
		settings.LLM.BaseURL = llm.BaseURL
	}
	if err := saveSettings(settings); err != nil {
		log.Printf("SaveLLMSettings save error: %v", err)
	}
	// P0-1 修复: 保存后立即回读解密自检, 防止因加密密钥缺失/漂移导致
	// "已保存但实际不可用"的静默失败(此前界面显示 configured=true, 实际 LLM 全部降级)。
	if llm.APIKey != "" {
		if verify := decryptAPIKey(settings.LLM.APIKey); verify == "" {
			settings.LLM.APIKey = ""
			_ = saveSettings(settings)
			settingsMu.Unlock()
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "API key 保存后自检解密失败(LLM_ENCRYPTION_KEY 缺失或不匹配), 已回滚配置, 请检查部署密钥",
			})
			return
		}
	}
	settingsMu.Unlock()

	// 保存历史版本
	go saveLLMHistory(settings.LLM)

	auditWrite(r, "settings.llm.update", settings.LLM.Provider, "更新 LLM 配置 model="+settings.LLM.Model)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "LLM settings saved",
		"data":    settings.LLM,
	})
}

// TestLLMConnection handles POST /api/v1/settings/llm/test
func (h *Handler) TestLLMConnection(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var testSettings map[string]string
	json.Unmarshal(body, &testSettings)

	apiKey := testSettings["api_key"]
	baseURL := testSettings["base_url"]
	model := testSettings["model"]
	providerID := testSettings["provider_id"]

	// 优先从指定 provider 读取加密 key（修复：测试 deepseek 等非启用 provider 时，
	// 不能误用全局默认 provider 的 key）
	testProviderName := ""
	if providerID != "" {
		testProviderName = getProviderName(providerID)
	}
	if apiKey == "" && providerID != "" {
		apiKey = getProviderEncryptedKey(providerID)
	}

	settingsMu.RLock()
	if apiKey == "" {
		apiKey = decryptAPIKey(settings.LLM.APIKey)
	}
	if baseURL == "" {
		baseURL = settings.LLM.BaseURL
	}
	if model == "" {
		model = settings.LLM.Model
	}
	if testProviderName == "" {
		testProviderName = settings.LLM.Provider
	}
	settingsMu.RUnlock()

	// 安全(P0-4)：base_url 非空时必须 https 且主机名非私网/metadata 地址，
	// 防止测试端点利用已保存的 API key 发起 SSRF 或把内网响应回传给攻击者
	if msg := validateLLMBaseURL(baseURL); msg != "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "base_url 校验失败: " + msg})
		return
	}

	if apiKey == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "API key not configured",
		})
		return
	}

	// Test connectivity via /models endpoint
	req, _ := http.NewRequest("GET", baseURL+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success":  false,
			"message":  err.Error(),
			"provider": testProviderName,
			"model":    model,
		})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success":     true,
			"message":     "Connection successful",
			"provider":    testProviderName,
			"model":       model,
			"http_status": resp.StatusCode,
		})
	} else {
		bodyStr := string(respBody)
		if len(bodyStr) > 500 {
			bodyStr = bodyStr[:500]
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success":     false,
			"message":     "API returned error",
			"provider":    testProviderName,
			"model":       model,
			"http_status": resp.StatusCode,
			"detail":      bodyStr,
		})
	}
}

// GetK8sSettings handles GET /api/v1/settings/k8s
func (h *Handler) GetK8sSettings(w http.ResponseWriter, r *http.Request) {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": settings.K8s,
	})
}

func (h *Handler) ModelsLLM(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]string
	json.Unmarshal(body, &req)

	apiKey := req["api_key"]
	baseURL := req["base_url"]
	if apiKey == "" || baseURL == "" {
		settingsMu.RLock()
		if apiKey == "" {
			apiKey = decryptAPIKey(settings.LLM.APIKey)
		}
		if baseURL == "" {
			baseURL = settings.LLM.BaseURL
		}
		settingsMu.RUnlock()
	}

	if apiKey == "" {
		respondJSON(w, 200, map[string]interface{}{"models": []string{}, "error": "no api key"})
		return
	}

	req2, _ := http.NewRequest("GET", baseURL+"/models", nil)
	req2.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := h.client.Do(req2)
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"models": []string{}, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var result struct {
		Data []struct{ ID string } `json:"data"`
	}
	if json.Unmarshal(data, &result) == nil && len(result.Data) > 0 {
		models := make([]string, len(result.Data))
		for i, m := range result.Data {
			models[i] = m.ID
		}
		respondJSON(w, 200, map[string]interface{}{"models": models, "provider": baseURL})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"models": []string{}, "raw": string(data)[:500]})
}

// GetLLMConfig returns the full (decrypted) LLM configuration for internal use.
func (h *Handler) GetLLMConfig() LLMSettings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	cfg := settings.LLM
	cfg.APIKey = decryptAPIKey(cfg.APIKey)
	return cfg
}

// ── ProxyAI 安全加固（G2）──
// restrictedProxyPaths 是需要 admin/approver 角色的高危代理路径前缀：
// NL2SQL（可执行 SQL）、shell（命令执行）、ipmi/node/snmp（硬件与节点操作）、
// ops（任务/审计/报告/变更）、ai/kg（知识图谱构建）。普通 user 角色一律 403。
var restrictedProxyPaths = []string{
	"/api/v1/ai/nl2sql",
	"/api/v1/ai/shell",
	"/api/v1/ipmi",
	"/api/v1/node",
	"/api/v1/snmp",
	"/api/v1/ops",
	"/api/v1/ai/kg",
}

// isRestrictedProxyPath 判断被代理路径是否属于高危面（需 admin/approver）。
func isRestrictedProxyPath(path string) bool {
	for _, p := range restrictedProxyPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

const (
	maxProxyBody     = 10 << 20 // 请求体上限 10MB
	maxProxyResponse = 50 << 20 // 响应体上限 50MB
)

func (h *Handler) ProxyAI(w http.ResponseWriter, r *http.Request) {
	// Delegated role/approval authority has not migrated to this proxy yet. High
	// risk legacy proxy paths therefore fail closed instead of accepting JWT claims.
	if isRestrictedProxyPath(r.URL.Path) && !hasPrivilegedRole(r) {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	// 用 orchestratorBase()（env 可覆盖），与 ProxyShellWS 一致，便于测试注入 mock。
	url := orchestratorBase() + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	// G2 安全加固：请求体大小上限（10MB），超限 413 拒绝，防止超大 body 占内存。
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read body"})
		return
	}
	if len(body) > maxProxyBody {
		respondJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{"error": "request body too large (>10MB)"})
		return
	}
	req, _ := http.NewRequest(r.Method, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// 注入 LLM 配置到被代理请求头。
	// 安全：不再把解密后的明文 API Key 放进 X-LLM-API-Key 头（避免明文 key 在服务间
	// HTTP 上传输）。orchestrator 检测不到该 header 时，会通过带 X-Internal-Token 的
	// 内部接口 /api/v1/settings/llm/internal 自行拉取已保存配置（见 orchestrator
	// _parse_llm_config / _fetch_saved_llm_config 回退逻辑）。
	llmCfg := h.GetLLMConfig()
	if llmCfg.Model != "" {
		req.Header.Set("X-LLM-Model", llmCfg.Model)
		req.Header.Set("X-LLM-Base-URL", llmCfg.BaseURL)
		req.Header.Set("X-LLM-Provider", llmCfg.Provider)
	}

	// The outbound request starts empty. Explicitly remove historical authority
	// headers so client input and JWT claims cannot be laundered into an internal
	// user, role, scope, approval, tenant, or signed-context assertion.
	for _, header := range []string{
		"X-Internal-User", "X-Internal-Role", "X-Internal-Approver", "X-Internal-Scope",
		"X-Trusted-Request-Context", "X-Tenant-ID",
	} {
		req.Header.Del(header)
	}
	// 注入内部服务共享 token（仅当已配置），供 orchestrator 校验请求确实来自可信的
	// query-api 代理（该代理已通过 AuthMiddleware 完成 JWT 鉴权与角色注入），
	// 防止绕过 query-api 直连 orchestrator 伪造 X-Internal-Role/Approver。
	if it := os.Getenv("INTERNAL_TOKEN"); it != "" {
		req.Header.Set("X-Internal-Token", it)
	}

	// QuotaAI 配额检查（P3-2b）：仅对 LLM 调用路径（/ai/chat、/ai/nl2sql、/ai/final_report）
	// 消耗配额。租户 QuotaAI>0 且当日已用达到上限 → 429 不转发；QuotaAI=0 或租户不存在
	// （默认 default 恒在）→ 不限。当日计数为进程内内存实现（重启归零，见 tenant_quota.go），
	// 生产可替换为 Redis/MySQL 计数表。
	if isLLMProxyPath(r.URL.Path) {
		tenant := extractTenantID(r)
		used := quotaUsedToday(tenant)
		if quota := tenantQuotaAI(tenant); quota > 0 && used >= quota {
			respondJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error":          "AI 调用已达当日配额上限（quota_ai_calls）",
				"tenant":         tenant,
				"quota_ai_calls": quota,
				"used_today":     used,
			})
			return
		}
		// 转发前计数 +1
		quotaIncrementToday(tenant)
	}

	// Use longer timeout for AI requests (full 14-node DAG with 5 LLM calls = 120-300s)
	aiClient := &http.Client{Timeout: 300 * time.Second}
	resp, err := aiClient.Do(req)
	if err != nil {
		respondJSON(w, 502, map[string]interface{}{"error": "ai-orchestrator unavailable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// G2 安全加固：响应体大小上限（50MB），超限截断，防止超大响应占内存。
	n, _ := io.Copy(w, io.LimitReader(resp.Body, maxProxyResponse+1))
	if n > maxProxyResponse {
		log.Printf("ProxyAI response truncated: path=%s size=%d > %d", r.URL.Path, n, maxProxyResponse)
	}
}
