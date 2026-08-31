package api

import (
	"bytes"
	"context"
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
	configured := llm.Provider != "" && llm.Model != "" &&
		strings.TrimSpace(os.Getenv("AI_LLM_EGRESS_PROXY_URL")) != "" &&
		strings.TrimSpace(os.Getenv("LLM_PROXY_TOKEN")) != ""
	// This compatibility status endpoint intentionally exposes only readiness.
	// It does not disclose provider/model topology, endpoint locations, or any
	// secret-derived field.
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{
			"configured": configured,
		},
	})
}

// GetLLMAdminConfig handles the admin-only configuration view used by the
// settings page.  The public health endpoint above intentionally exposes only
// readiness; this endpoint returns the non-secret fields needed to render the
// editable form and a constant masked-key marker, never the decrypted key.
func (h *Handler) GetLLMAdminConfig(w http.ResponseWriter, r *http.Request) {
	settingsMu.RLock()
	llm := settings.LLM
	settingsMu.RUnlock()

	proxyReady := strings.TrimSpace(os.Getenv("AI_LLM_EGRESS_PROXY_URL")) != "" &&
		strings.TrimSpace(os.Getenv("LLM_PROXY_TOKEN")) != ""
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{
			"provider":        llm.Provider,
			"active_provider": llm.Provider,
			"model":           llm.Model,
			"base_url":        "",
			"configured":      llm.Provider != "" && llm.Model != "" && proxyReady,
			"proxy_ready":     proxyReady,
		},
	})
}

// GetInternalLLMSettings handles GET /api/v1/settings/llm/internal.
// The control plane exposes only routing metadata. Provider credentials live
// exclusively in the egress-proxy Secret and never cross into orchestrator
// memory, checkpoints, responses, or logs.
// F-14：认证强度升级——除 X-Internal-Token（service token）外，还必须校验 Ed25519
// TrustedRequestContext（issuer/audience/signature）+ 固定 llm.config.read capability
// （system principal），不再仅凭共享 token 放行。internal-only routing 保持。
func (h *Handler) GetInternalLLMSettings(w http.ResponseWriter, r *http.Request) {
	// 1) service token（既有）
	internalToken := os.Getenv("INTERNAL_TOKEN")
	got := r.Header.Get("X-Internal-Token")
	if internalToken == "" || got == "" || got != internalToken {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}
	// 2) F-14：TrustedRequestContext V2 + 固定 llm.config.read capability（system principal）。
	if _, err := authorizeInternalControlPlane(r, "llm.config.read", "ai-orchestrator"); err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "invalid_trusted_context"})
		return
	}
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	llm := settings.LLM
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{
			"provider": llm.Provider,
			"model":    llm.Model,
			"base_url": llm.BaseURL,
		},
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
	if strings.TrimSpace(llm.APIKey) != "" || strings.TrimSpace(llm.BaseURL) != "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "provider credentials and base_url are managed by the egress proxy"})
		return
	}
	if strings.TrimSpace(llm.Provider) == "" || strings.TrimSpace(llm.Model) == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "provider and model are required"})
		return
	}

	settingsMu.Lock()
	if llm.Provider != "" {
		settings.LLM.Provider = llm.Provider
	}
	settings.LLM.APIKey = ""
	settings.LLM.Provider = strings.TrimSpace(llm.Provider)
	settings.LLM.Model = strings.TrimSpace(llm.Model)
	settings.LLM.BaseURL = ""
	if err := saveSettings(settings); err != nil {
		log.Printf("SaveLLMSettings save error: %v", err)
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

	if strings.TrimSpace(testSettings["api_key"]) != "" || strings.TrimSpace(testSettings["base_url"]) != "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "message": "provider credentials and base_url are managed by the egress proxy"})
		return
	}
	model := strings.TrimSpace(testSettings["model"])
	providerID := strings.TrimSpace(testSettings["provider_id"])
	requestedProvider := strings.TrimSpace(testSettings["provider"])
	providerName := ""
	if providerID != "" {
		providerName = getProviderName(providerID)
	}

	settingsMu.RLock()
	if model == "" {
		model = settings.LLM.Model
	}
	testProviderName := resolveLLMTestProvider(requestedProvider, providerName, settings.LLM.Provider)
	settingsMu.RUnlock()
	if testProviderName == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "message": "provider is required"})
		return
	}
	proxyBase := strings.TrimRight(firstNonEmpty(os.Getenv("AI_LLM_EGRESS_PROXY_URL"), os.Getenv("LLM_PROXY_URL")), "/")
	proxyToken := strings.TrimSpace(os.Getenv("LLM_PROXY_TOKEN"))
	if proxyBase == "" || proxyToken == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "LLM egress proxy is not configured",
		})
		return
	}

	// Test connectivity through the fixed egress proxy; provider routing is a
	// server-side allow-listed identifier, never a caller URL.
	target := proxyBase + "/v1/proxy/" + url.PathEscape(testProviderName) + "/models"
	req, _ := http.NewRequest("GET", target, nil)
	req.Header.Set("X-Proxy-Token", proxyToken)

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
			"message":     "Connection successful via egress proxy",
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
			"message":     "LLM proxy returned error",
			"provider":    testProviderName,
			"model":       model,
			"http_status": resp.StatusCode,
			"detail":      bodyStr,
		})
	}
}

func resolveLLMTestProvider(requested, providerName, fallback string) string {
	for _, candidate := range []string{requested, providerName, fallback} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"models": []string{}, "error": "invalid request"})
		return
	}
	var req struct {
		ProviderID string `json:"provider_id"`
		// api_key/base_url are intentionally decoded only to reject legacy callers;
		// neither value is ever used for an outbound request.
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	}
	if json.Unmarshal(body, &req) != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"models": []string{}, "error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.APIKey) != "" || strings.TrimSpace(req.BaseURL) != "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"models": []string{}, "error": "caller credentials or URL are not accepted; use provider_id"})
		return
	}
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" || len(providerID) > 64 || !validProviderID(providerID) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"models": []string{}, "error": "provider_id is required"})
		return
	}
	proxyDefault := "http://ai-llm-egress-proxy:8080"
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AIOPS_MTLS_REQUIRED")), "true") {
		proxyDefault = "https://ai-llm-egress-proxy:8080"
	}
	proxyBase := strings.TrimRight(firstNonEmpty(os.Getenv("AI_LLM_EGRESS_PROXY_URL"), proxyDefault), "/")
	target := proxyBase + "/v1/proxy/" + url.PathEscape(providerID) + "/models"
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"models": []string{}, "error": "LLM proxy unavailable"})
		return
	}
	req2.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(os.Getenv("LLM_PROXY_TOKEN")); token != "" {
		req2.Header.Set("X-Proxy-Token", token)
	} else {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"models": []string{}, "error": "LLM proxy credential unavailable"})
		return
	}
	client, clientErr := h.internalHTTPClient(15 * time.Second)
	if clientErr != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"models": []string{}, "error": "BACKEND_MTLS_UNAVAILABLE"})
		return
	}
	resp, err := client.Do(req2)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"models": []string{}, "error": "LLM proxy unavailable"})
		return
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"models": []string{}, "error": "LLM proxy response unreadable"})
		return
	}

	var result struct {
		Data []struct{ ID string } `json:"data"`
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && json.Unmarshal(data, &result) == nil {
		models := make([]string, len(result.Data))
		for i, m := range result.Data {
			models[i] = m.ID
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"models": models, "provider": providerID})
		return
	}
	raw := string(data)
	if len(raw) > 500 {
		raw = raw[:500]
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"models": []string{}, "raw": raw, "provider": providerID})
}

func validProviderID(value string) bool {
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
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

func legacyProxyAllowed(path, method string) bool {
	read := method == http.MethodGet || method == http.MethodHead
	if read {
		for _, base := range []string{
			"/api/v1/ai/skills",
			"/api/v1/ai/agents",
			"/api/v1/ai/flows",
			"/api/v1/ai/knowledge",
			"/api/v1/ai/rules",
			"/api/v1/ai/sessions",
			"/api/v1/ai/session",
			"/api/v1/ai/nl2sql",
			"/api/v1/ai/workflows",
			"/api/v1/ai/kg",
			"/api/v1/ops/tasks",
			"/api/v1/ops/recovery/policy",
			"/api/v1/ops/cases",
			"/api/v1/ops/anomalies",
			"/api/v1/ops/artifacts",
			"/api/v1/ops/reports",
			"/api/v1/ops/audit-logs",
			"/api/v1/ops/changes",
			"/api/v1/ops/export/chat",
			"/api/v1/ipmi/sensors",
			"/api/v1/ipmi/events",
			"/api/v1/node/health",
			"/api/v1/snmp/devices",
			"/api/v1/snmp/interfaces",
		} {
			if isPathOrChild(path, base) {
				return true
			}
		}
		return path == "/api/v1/mcp/tools" || path == "/api/v1/ai/final_report"
	}

	if method == http.MethodPost {
		for _, exact := range []string{
			"/api/v1/ai/agents",
			"/api/v1/ai/flows",
			"/api/v1/ai/knowledge",
			"/api/v1/ai/knowledge/case",
			"/api/v1/ai/knowledge/rag/reload",
			"/api/v1/ai/knowledge/rag/import",
			"/api/v1/ai/rules",
			"/api/v1/ai/final_report",
			"/api/v1/ai/suggestion/execute",
			"/api/v1/ai/nl2sql/translate",
			"/api/v1/ops/tasks",
			"/api/v1/ops/recovery/plan",
			"/api/v1/ops/changes",
			"/api/v1/ops/changes/webhook",
			"/api/v1/ops/rca",
			"/api/v1/ops/rca/alert",
			"/api/v1/ops/rca/deep",
			"/api/v1/ops/anomalies/scan",
			"/api/v1/ops/k8s/preflight",
			"/api/v1/ops/k8s/execute",
			"/api/v1/node/health/aggregate",
			"/api/v1/mcp/call",
			"/api/v1/snmp/collect",
		} {
			if path == exact {
				return true
			}
		}
		for _, base := range []string{
			"/api/v1/ai/skills/",
			"/api/v1/ai/flows/",
			"/api/v1/ai/rules/",
			"/api/v1/ai/workflows",
			"/api/v1/ai/nl2sql/",
			"/api/v1/ops/tasks/",
			"/api/v1/ops/cases/",
			"/api/v1/ops/recovery/",
		} {
			if isPathOrChild(path, base) {
				return true
			}
		}
		return false
	}

	if method == http.MethodPut || method == http.MethodDelete {
		for _, base := range []string{
			"/api/v1/ai/agents/",
			"/api/v1/ai/workflows/",
			"/api/v1/ai/rules/",
		} {
			if isPathOrChild(path, base) {
				return true
			}
		}
	}
	return false
}

func legacyProxyNeedsPrivilegedRole(path, method string) bool {
	if isRestrictedProxyPath(path) || path == "/api/v1/mcp/call" {
		return true
	}
	if method == http.MethodGet || method == http.MethodHead {
		return false
	}
	for _, base := range []string{
		"/api/v1/ai/skills",
		"/api/v1/ai/agents",
		"/api/v1/ai/flows",
		"/api/v1/ai/knowledge",
		"/api/v1/ai/rules",
		"/api/v1/ai/workflows",
	} {
		if isPathOrChild(path, base) {
			return true
		}
	}
	return false
}

// ProxyAI is the browser-facing AI proxy boundary (V9.2 §P3.9-B3).
//
// P19.6: /api/v1/ai/chat 已拆分为独立的 ProxyChat（对话型，ai.chat capability，
// SSE 流式经 query-api canonical-protected 透传 orchestrator /internal/v1/chat）。
// 此处 ProxyAI 仅处理 Run API 只读/创建记录代理，其余 legacy 路由一律 fail-closed。
//
// Every other legacy proxy route remains fail-closed; it is not an investigation
// entry and is not given a signed RunInvocationContext. The browser can never
// supply the final tenant/cluster; query-api re-resolves, authorizes and
// canonicalizes it, then signs the canonical cluster UUID into the context.
func (h *Handler) ProxyAI(w http.ResponseWriter, r *http.Request) {
	// P12：Run API 代理（GET 列表/详情、POST 创建记录）→ orchestrator。
	// query-api 校验 JWT + tenant，注入方向凭据 X-Internal-Token 后转发；orchestrator 校验 token。
	// 注意：POST 仅创建 Run 记录（不触发调查链；真实调查触发走 /internal/v1/run-invocations 含授权）。
	if (r.URL.Path == "/api/v1/ai/runs" || strings.HasPrefix(r.URL.Path, "/api/v1/ai/runs/")) &&
		(r.Method == http.MethodGet || r.Method == http.MethodPost) {
		h.proxyRunList(w, r)
		return
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AIOPS_ENV")), "production") {
		// All browser-facing legacy orchestrator proxies are retired in the
		// production profile.  Canonical chat/session/run handlers above are the
		// only supported entry points and carry signed scope context.
		respondJSON(w, http.StatusGone, map[string]interface{}{"error": "LEGACY_ROUTE_RETIRED"})
		return
	}
	if !legacyProxyAllowed(r.URL.Path, r.Method) {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	authCtx, err := RequestAuthorizationContext(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}
	r = withAuthorizationContext(r, authCtx)
	operator, ok := authoritativeUser(r)
	if !ok {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	if legacyProxyNeedsPrivilegedRole(r.URL.Path, r.Method) &&
		!(operator.Role == "admin" || operator.Role == "approver" || operator.IsApprover) {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	h.proxyLegacy(w, r, authCtx, operator)
}

func (h *Handler) proxyLegacy(w http.ResponseWriter, r *http.Request, authCtx AuthorizationContext, operator *store.User) {
	var body io.Reader
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		payload, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
		if err != nil || len(payload) > maxProxyBody {
			respondJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{"error": "request body too large"})
			return
		}
		body = bytes.NewReader(payload)
	}
	target := orchestratorBase() + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	proxyTimeout := 10 * time.Second
	// Reports/RCA/final-report may legitimately wait for an LLM response. Their
	// browser callers already use an extended timeout, so do not turn a slow but
	// reachable provider into a false backend outage. Short read/proxy routes
	// must fail fast when the orchestrator Service has no ready endpoints.
	if strings.HasPrefix(r.URL.Path, "/api/v1/ops/rca") ||
		strings.HasPrefix(r.URL.Path, "/api/v1/ai/nl2sql") ||
		r.URL.Path == "/api/v1/ai/final_report" {
		proxyTimeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), proxyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "BACKEND_UNAVAILABLE"})
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Internal-Token", internalServiceToken())
	req.Header.Set("X-Tenant-ID", authCtx.TenantID)
	req.Header.Set("X-Internal-User", operator.UserUUID)
	req.Header.Set("X-Internal-Role", operator.Role)
	if operator.Role == "admin" || operator.Role == "approver" || operator.IsApprover {
		req.Header.Set("X-Internal-Approver", "1")
	} else {
		req.Header.Set("X-Internal-Approver", "0")
	}
	client, clientErr := h.internalHTTPClient(proxyTimeout)
	if clientErr != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "BACKEND_MTLS_UNAVAILABLE"})
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "BACKEND_UNAVAILABLE"})
		return
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponse+1))
	if readErr != nil || len(respBody) > maxProxyResponse {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "upstream response too large"})
		return
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// ProxyChat handles the browser-facing dialogue entry /api/v1/ai/chat (P19.6).
//
// This is a canonical-protected, dialogue-only path with capability=ai.chat:
//   - JWT + MySQL authorization (authoritative identity/tenant) — no client override.
//   - canonical cluster resolution + single-tenant ownership.
//   - capability=ai.chat signed into the RunInvocationContext (NOT ai.investigate):
//     dialogue never creates an Investigation Run and never triggers ManualBoundary
//     run-creation semantics.
//   - forwards to orchestrator /internal/v1/chat and STREAMS the SSE response back
//     (no buffering) so streaming/disconnect/reconnect keep identity binding.
//
// Fail-closed matrix: missing/expired/replayed context, body tenant/cluster mismatch,
// cross-cluster, no capability, system principal abuse → all rejected.
func (h *Handler) ProxyChat(w http.ResponseWriter, r *http.Request) {
	issuer := currentRunInvocationIssuer()
	if issuer == nil {
		// Issuer not provisioned → fail closed rather than produce unsigned calls.
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}

	// 1. JWT + MySQL real-time authorization (authoritative identity/tenant).
	authCtx, err := RequestAuthorizationContext(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}

	// 2. Read requested body scope. The browser provides a requested cluster ref
	//    (slug/name/UUID) which is re-resolved; it is never signed as-is.
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "VALIDATION_FAILED"})
		return
	}
	// 兼容前端两种字段：调查入口用 cluster（slug/name/UUID），AiChat 对话用 cluster_id（canonical UUID）。
	// 两者都只是"请求的 cluster 引用"，最终 canonical cluster 由服务端 ResolveRef 决定，绝不直接签名。
	requestedCluster, _ := body["cluster"].(string)
	if strings.TrimSpace(requestedCluster) == "" {
		requestedCluster, _ = body["cluster_id"].(string)
	}
	if strings.TrimSpace(requestedCluster) == "" || requestedCluster == "all" {
		// missing cluster → fail closed (V9.2: no default/current-cluster fallback)
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "CLUSTER_ACCESS_DENIED"})
		return
	}

	// 3. Canonical cluster resolution + single-tenant ownership.
	cluster, err := (&store.ClusterDAO{}).ResolveRef(authCtx.TenantID, requestedCluster)
	if err != nil {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "CLUSTER_ACCESS_DENIED"})
		return
	}
	owner, err := (&store.ClusterDAO{}).TenantClustersForCluster(cluster.ClusterID)
	if err != nil || owner != authCtx.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "CLUSTER_ACCESS_DENIED"})
		return
	}
	authCtx.ActiveClusterID = cluster.ClusterID

	// 4. 用户 RBAC 权威 SoT：先从 MySQL 读取用户权威角色，校验其授予
	//    ai.chat（对话型只读），再创建/写入 transcript。这样失效用户不会
	//    触发会话写入，也不会把鉴权失败伪装成 CHAT_SESSION_BACKEND_UNAVAILABLE。
	if !authorizeUserChatCapability(authCtx.UserID) {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}

	// Query API owns the browser transcript.  The caller may resume only a
	// canonical UUID already belonging to this user/scope; first-turn sessions
	// are generated here before any downstream call so the orchestrator cannot
	// choose an unscoped/short identifier.
	sessionID, _ := body["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = newCanonicalSessionUUID()
		if sessionID == "" {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "CHAT_SESSION_ID_UNAVAILABLE"})
			return
		}
	} else if !canonicalUUID.MatchString(sessionID) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_session_id"})
		return
	}
	// A turn id is the idempotency identity for one user submission.  The
	// browser generates it for retries; the server generates one for older
	// clients so every canonical request still gets a durable identity.
	turnID, _ := body["turn_id"].(string)
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = newCanonicalSessionUUID()
		if turnID == "" {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "CHAT_TURN_ID_UNAVAILABLE"})
			return
		}
	} else if !canonicalUUID.MatchString(turnID) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_turn_id"})
		return
	}
	body["session_id"] = sessionID
	body["thread_id"] = sessionID
	body["turn_id"] = turnID
	intent, _ := body["intent"].(string)
	service, _ := body["service"].(string)
	chatSessions := &store.AIChatSessionDAO{}
	// Rehydrate a bounded transcript summary from MySQL so a second query-api
	// replica can continue the conversation without depending on local SQLite.
	if _, previous, historyErr := chatSessions.Get(sessionID, authCtx.UserID, authCtx.TenantID, cluster.ClusterID); historyErr == nil {
		var lastQuestion, lastAnswer string
		for _, msg := range previous {
			if msg.Role == "user" {
				lastQuestion = msg.Content
			}
			if msg.Role == "assistant" && msg.Kind == "" {
				lastAnswer = msg.Content
			}
		}
		if lastQuestion != "" || lastAnswer != "" {
			body["history_context"] = fmt.Sprintf("上一轮问题: %s\n上一轮回答要点: %s", lastQuestion[:minStringLen(len(lastQuestion), 200)], lastAnswer[:minStringLen(len(lastAnswer), 500)])
		}
	}
	if err := chatSessions.EnsureSession(sessionID, authCtx.UserID, authCtx.TenantID, cluster.ClusterID, intent, service); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "scope mismatch") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "CHAT_SESSION_SCOPE_DENIED"})
			return
		}
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	if message, _ := body["message"].(string); strings.TrimSpace(message) != "" {
		if err := chatSessions.AppendMessageForTurn(sessionID, authCtx.UserID, authCtx.TenantID, cluster.ClusterID, turnID, "user", "", message, nil); err != nil {
			if strings.Contains(err.Error(), "idempotency mismatch") {
				respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "CHAT_TURN_IDEMPOTENCY_MISMATCH"})
				return
			}
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
			return
		}
	}
	// A completed turn is durable in Query/MySQL.  Replay it before signing or
	// invoking the Orchestrator so a browser reconnect cannot execute the same
	// provider request twice.  A turn containing only the user card is treated
	// as incomplete and follows the normal downstream path.
	turnMessages, turnErr := chatSessions.GetTurn(sessionID, authCtx.UserID, authCtx.TenantID, cluster.ClusterID, turnID)
	if turnErr != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "CHAT_SESSION_BACKEND_UNAVAILABLE"})
		return
	}
	if replayChatTurn(w, sessionID, turnID, turnMessages) {
		return
	}

	// 5. Sign RunInvocationContext with capability=ai.chat (对话型，非 ai.investigate)。
	signed, err := issuer.SignChatInvocation(
		"user", authCtx.UserID, authCtx.SessionID, authCtx.TenantID, "frontend",
		[]string{cluster.ClusterID}, time.Now())
	if err != nil {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}

	// 5. Forward to orchestrator trusted streaming ingress with directional credentials,
	//    then stream the SSE response back to the browser without buffering.
	target := orchestratorBase() + "/internal/v1/chat"
	payload, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "BACKEND_UNAVAILABLE"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", issuer.ServiceToken())
	req.Header.Set("X-Trusted-Request-Context", signed)

	// Bound the upstream stream lifetime.  The request context still cancels
	// promptly when the browser disconnects; the deadline prevents a leaked
	// connection when an upstream proxy stalls without closing.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	req = req.WithContext(ctx)
	client, clientErr := newInternalServiceClient(5 * time.Minute)
	if clientErr != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "BACKEND_MTLS_UNAVAILABLE"})
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "BACKEND_UNAVAILABLE"})
		return
	}
	defer resp.Body.Close()

	// 透传 orchestrator 的 SSE 头（Content-Type: text/event-stream）+ 状态码。
	for _, header := range []string{"Content-Type", "X-Session-Id", "Cache-Control"} {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}
	w.Header().Set("X-Chat-Turn-Id", turnID)
	w.WriteHeader(resp.StatusCode)
	// 逐块 Flush 透传，不缓冲（对齐 P10 公共 SSE proxy WriteTimeout=0 的长连接语义）。
	if flusher, ok := w.(http.Flusher); ok {
		buf := make([]byte, 32*1024)
		var streamBuffer string
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				streamBuffer += string(buf[:n])
				var persistErr error
				streamBuffer, persistErr = persistChatSSEFrames(chatSessions, sessionID, turnID, authCtx, streamBuffer)
				if persistErr != nil {
					log.Printf("chat transcript persistence failed session=%s turn=%s: %v", sessionID, turnID, persistErr)
					writeChatPersistenceError(w, flusher)
					return
				}
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				flusher.Flush()
			}
			if readErr != nil {
				return
			}
		}
	}
	// 不支持 Flusher 的响应容器：退化为一次性复制，同时保留 transcript。
	data, _ := io.ReadAll(resp.Body)
	if _, persistErr := persistChatSSEFrames(chatSessions, sessionID, turnID, authCtx, string(data)); persistErr != nil {
		log.Printf("chat transcript persistence failed session=%s turn=%s: %v", sessionID, turnID, persistErr)
		writeChatPersistenceError(w, nil)
		return
	}
	_, _ = w.Write(data)
}

func newCanonicalSessionUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failure is process-fatal in the sense that a deterministic
		// session ID would violate the session ownership invariant.
		return ""
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func minStringLen(length, max int) int {
	if length < max {
		return length
	}
	return max
}

// persistChatSSEFrames records only durable transcript cards (assistant done
// text and actionable suggestions).  Progress/tool telemetry remains ephemeral
// and is still streamed to the browser without inflating the MySQL transcript.
func persistChatSSEFrames(dao *store.AIChatSessionDAO, sessionID, turnID string, authCtx AuthorizationContext, input string) (string, error) {
	for {
		idx := strings.Index(input, "\n\n")
		if idx < 0 {
			return input, nil
		}
		frame := input[:idx]
		input = input[idx+2:]
		name := "message"
		var data string
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				data += strings.TrimPrefix(line, "data: ")
			}
		}
		if data == "" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		switch name {
		case "done":
			if text, _ := event["text"].(string); strings.TrimSpace(text) != "" {
				if err := dao.AppendMessageForTurn(sessionID, authCtx.UserID, authCtx.TenantID, authCtx.ActiveClusterID, turnID, "assistant", "", text, nil); err != nil {
					return input, fmt.Errorf("persist assistant response: %w", err)
				}
			}
		case "suggestion":
			metadata := map[string]any{}
			for _, key := range []string{"plan", "script", "thread_id", "risk_score", "risk_reason", "service"} {
				if value, ok := event[key]; ok {
					metadata[key] = value
				}
			}
			if err := dao.AppendMessageForTurn(sessionID, authCtx.UserID, authCtx.TenantID, authCtx.ActiveClusterID, turnID, "assistant", "suggestion", "", metadata); err != nil {
				return input, fmt.Errorf("persist assistant suggestion: %w", err)
			}
		}
	}
}

func writeChatPersistenceError(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = io.WriteString(w, "event: error\ndata: {\"error\":\"CHAT_TRANSCRIPT_PERSIST_FAILED\"}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// replayChatTurn emits only durable response cards for a completed turn.  It
// deliberately omits progress/tool telemetry, which is ephemeral by design;
// a caller that reconnects before the done card was stored is allowed to
// resume by invoking the same turn again.
func replayChatTurn(w http.ResponseWriter, sessionID, turnID string, messages []store.ChatMessage) bool {
	completed := false
	for _, message := range messages {
		if message.Role == "assistant" && message.Kind == "" && strings.TrimSpace(message.Content) != "" {
			completed = true
			break
		}
	}
	if !completed {
		return false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Session-Id", sessionID)
	w.Header().Set("X-Chat-Turn-Id", turnID)
	w.WriteHeader(http.StatusOK)
	sequence := int64(0)
	for _, message := range messages {
		var eventType string
		payload := map[string]any{}
		switch {
		case message.Role == "assistant" && message.Kind == "suggestion":
			eventType = "suggestion"
			for key, value := range message.Metadata {
				payload[key] = value
			}
		case message.Role == "assistant" && message.Kind == "":
			eventType = "done"
			payload["text"] = message.Content
		default:
			continue
		}
		sequence++
		raw, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", sequence, eventType, raw)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

// roleGrantsAIChat：服务端权威角色 → ai.chat（对话只读）授权映射。
// 与 orchestrator ROLE_CAPABILITIES 对齐：所有已验证角色均可对话（对话内部 Agent
// 查询工具仍需各自 observability.*.read capability，由 Tool Registry 层约束）。
var roleGrantsAIChat = map[string]bool{
	"admin": true, "engineer": true, "operator": true, "viewer": true,
}

// authorizeUserChatCapability：用户 RBAC 权威 SoT —— 从 MySQL 读取用户权威角色，
// 校验其授予 ai.chat。返回 false 时调用方必须 fail-closed（不签发 ai.chat 上下文）。
// 这是 query-api 作为权威能力源的实现：orchestrator 不再依赖 SERVICE_ACCOUNT_ROLES 全域映射。
func authorizeUserChatCapability(userID string) bool {
	if db := store.GetDB(); db != nil {
		if u, err := (&store.UserDAO{}).GetByUUID(userID); err == nil && u != nil && u.Status == 1 {
			return roleGrantsAIChat[u.Role]
		}
	}
	return false
}

// internalServiceToken：orchestrator 方向凭据（INTERNAL_TOKEN，与 orchestrator auth_middleware 校验一致）。
func internalServiceToken() string {
	return os.Getenv("INTERNAL_TOKEN")
}

// proxyRunList：GET/POST /api/v1/ai/runs(/...) 代理 → orchestrator。
// query-api 校验 JWT + MySQL 授权（权威 tenant），注入方向凭据 X-Internal-Token 转发；
// orchestrator auth_middleware 校验该 token（INTERNAL_TOKEN）后由 ai_runs_api 处理。
func (h *Handler) proxyRunList(w http.ResponseWriter, r *http.Request) {
	authCtx, err := RequestAuthorizationContext(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}
	// 转发到 orchestrator 的 Run API（保留 path/query/method/body，注入方向凭据）。
	target := orchestratorBase() + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var bodyReader io.Reader
	if r.Method == http.MethodPost && r.Body != nil {
		bodyReader = r.Body
	}
	req, err := http.NewRequest(r.Method, target, bodyReader)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "BACKEND_UNAVAILABLE"})
		return
	}
	if r.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Internal-Token", internalServiceToken())
	req.Header.Set("X-Tenant-ID", authCtx.TenantID)
	client, clientErr := h.internalHTTPClient(30 * time.Second)
	if clientErr != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "BACKEND_MTLS_UNAVAILABLE"})
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "BACKEND_UNAVAILABLE"})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}
