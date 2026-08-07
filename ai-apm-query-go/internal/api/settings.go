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
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var llmEncryptionKey = func() []byte {
	if k := os.Getenv("LLM_ENCRYPTION_KEY"); k != "" {
		// Pad or truncate to 32 bytes for AES-256
		b := []byte(k)
		key := make([]byte, 32)
		copy(key, b)
		return key
	}
	return nil // no encryption if key not set
}()

func encryptAPIKey(plaintext string) string {
	if llmEncryptionKey == nil || plaintext == "" {
		return plaintext
	}
	block, err := aes.NewCipher(llmEncryptionKey)
	if err != nil {
		return plaintext
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return plaintext
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plaintext
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func decryptAPIKey(encoded string) string {
	if llmEncryptionKey == nil || encoded == "" {
		return encoded
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return encoded // not encrypted, return as-is
	}
	block, err := aes.NewCipher(llmEncryptionKey)
	if err != nil {
		return encoded
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return encoded
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return encoded
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return encoded
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
	settings     *Settings
	settingsMu   sync.RWMutex
	settingsPath = "/tmp/observability-settings.json"
)

func init() {
	if f := os.Getenv("SETTINGS_FILE"); f != "" {
		settingsPath = f
	}
	settings = loadSettings()
}

func loadSettings() *Settings {
	s := &Settings{
		LLM: LLMSettings{Provider: "openai", Model: "gpt-4o", BaseURL: "https://api.openai.com/v1"},
		K8s: K8sSettings{Enabled: true, AuthType: "ServiceAccount"},
	}
	if data, err := loadFromClickHouse("llm_settings"); err == nil && data != "" {
		json.Unmarshal([]byte(data), &s.LLM)
	}
	if data, err := loadFromClickHouse("k8s_settings"); err == nil && data != "" {
		json.Unmarshal([]byte(data), &s.K8s)
	}
	return s
}

func saveSettings(s *Settings) error {
	llmData, _ := json.Marshal(s.LLM)
	saveToClickHouse("llm_settings", string(llmData))
	k8sData, _ := json.Marshal(s.K8s)
	saveToClickHouse("k8s_settings", string(k8sData))
	return nil
}

func loadFromClickHouse(key string) (string, error) {
	return loadFromClickHouseWithRetry(key, 3)
}

func loadFromClickHouseWithRetry(key string, retries int) (string, error) {
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }
	urlStr := fmt.Sprintf("http://%s:%s/?query=SELECT+value+FROM+observability.platform_settings+FINAL+WHERE+key%%3D%%27%s%%27", chHost, chPort, key)

	var lastErr error
	for i := 0; i < retries; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second) // ClickHouse 可能还在启动中
		}
		resp, err := http.Get(urlStr)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			return lines[0], nil
		}
		// 空结果 = 还没写入过 settings，不算错误
		return "", fmt.Errorf("not found")
	}
	return "", lastErr
}

func saveToClickHouse(key, value string) error {
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }
	escaped := strings.ReplaceAll(value, "'", "\\'")
	query := fmt.Sprintf("INSERT INTO observability.platform_settings (key, value) VALUES ('%s', '%s')", key, escaped)
	url := fmt.Sprintf("http://%s:%s/?query=%s", chHost, chPort, url.QueryEscape(query))
	resp, err := http.Post(url, "text/plain", nil)
	if err != nil { return err }
	resp.Body.Close()
	return nil
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
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }

	// Get current max version
	verURL := fmt.Sprintf("http://%s:%s/?query=SELECT+coalesce(max(version),0)+FROM+observability.llm_config_history+FINAL+WHERE+provider%%3D%%27%s%%27", chHost, chPort, url.QueryEscape(llm.Provider))
	ver := 0
	if resp, err := http.Get(verURL); err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		fmt.Sscanf(strings.TrimSpace(string(body)), "%d", &ver)
	}
	ver++

	// Hash API Key (don't store raw)
	apiKeyHash := "****"
	if llm.APIKey != "" {
		h := sha256Hash(llm.APIKey)
		apiKeyHash = "sha256:" + h[:16]
	}

	escaped := strings.ReplaceAll(llm.BaseURL, "'", "\\'")
	query := fmt.Sprintf("INSERT INTO observability.llm_config_history (version, provider, model, base_url, api_key_hash, comment) VALUES (%d, '%s', '%s', '%s', '%s', 'config save')",
		ver, llm.Provider, llm.Model, escaped, apiKeyHash)
	insURL := fmt.Sprintf("http://%s:%s/", chHost, chPort)
	if resp, err := http.Post(insURL, "text/plain", bytes.NewBufferString(query)); err == nil {
		resp.Body.Close()
	}
}

// ListLLMHistory handles GET /api/v1/settings/llm/history
func (h *Handler) ListLLMHistory(w http.ResponseWriter, r *http.Request) {
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }

	qURL := fmt.Sprintf("http://%s:%s/?query=SELECT+version,provider,model,base_url,toString(created_at),operator,comment+FROM+observability.llm_config_history+ORDER+BY+version+DESC+LIMIT+20", chHost, chPort)
	resp, err := http.Get(qURL)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": "clickhouse unavailable"})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")

	// Get current config for comparison
	settingsMu.RLock()
	current := settings.LLM
	settingsMu.RUnlock()

	history := []LLMConfigHistory{}
	for _, line := range lines {
		if line == "" { continue }
		fields := strings.Split(line, "\t")
		if len(fields) < 7 { continue }
		var v int
		fmt.Sscanf(fields[0], "%d", &v)
		history = append(history, LLMConfigHistory{
			Version:   v,
			Provider:  fields[1],
			Model:     fields[2],
			BaseURL:   fields[3],
			CreatedAt: fields[4],
			Operator:  fields[5],
			Comment:   fields[6],
			IsCurrent: fields[1] == current.Provider && fields[2] == current.Model && fields[3] == current.BaseURL,
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
	var ver int
	fmt.Sscanf(parts[0], "%d", &ver)

	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }

	qURL := fmt.Sprintf("http://%s:%s/?query=SELECT+provider,model,base_url+FROM+observability.llm_config_history+WHERE+version%%3D%d+LIMIT+1", chHost, chPort, ver)
	resp, err := http.Get(qURL)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": "clickhouse unavailable"})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	line := strings.TrimSpace(string(body))
	if line == "" {
		respondJSON(w, 404, map[string]interface{}{"error": "version not found"})
		return
	}
	fields := strings.Split(line, "\t")
	if len(fields) < 3 {
		respondJSON(w, 500, map[string]interface{}{"error": "invalid history record"})
		return
	}

	// Restore provider/model/base_url, keep current API Key
	settingsMu.Lock()
	settings.LLM.Provider = fields[0]
	settings.LLM.Model = fields[1]
	settings.LLM.BaseURL = fields[2]
	settingsMu.Unlock()

	saveSettings(settings)
	respondJSON(w, 200, map[string]interface{}{
		"message": "rolled back",
		"provider": fields[0],
		"model":    fields[1],
		"base_url": fields[2],
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
	// Mask API key for display
	display := settings.LLM
	if len(display.APIKey) > 4 {
		display.APIKey = display.APIKey[:4] + "***" + display.APIKey[len(display.APIKey)-4:]
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": display,
	})
}

// GetInternalLLMSettings handles GET /api/v1/settings/llm/internal
// 仅供内部服务(ai-orchestrator)使用，返回解密后的真实 API Key。
// 通过 X-Internal-Token 鉴权。
func (h *Handler) GetInternalLLMSettings(w http.ResponseWriter, r *http.Request) {
	// 鉴权：X-Internal-Token 必须匹配
	if r.Header.Get("X-Internal-Token") != os.Getenv("INTERNAL_TOKEN") {
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
		settings.LLM.APIKey = encryptAPIKey(llm.APIKey)
	}
	if llm.Model != "" {
		settings.LLM.Model = llm.Model
	}
	if llm.BaseURL != "" {
		settings.LLM.BaseURL = llm.BaseURL
	}
	if err := saveSettings(settings); err != nil {
		log.Printf("SaveLLMSettings save error: %v", err)
	}
	settingsMu.Unlock()

	// 保存历史版本
	go saveLLMHistory(settings.LLM)

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
		if apiKey == "" { apiKey = settings.LLM.APIKey }
		if baseURL == "" { baseURL = settings.LLM.BaseURL }
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
		for i, m := range result.Data { models[i] = m.ID }
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

func (h *Handler) ProxyAI(w http.ResponseWriter, r *http.Request) {
	url := "http://ai-orchestrator.observability.svc.cluster.local:8080" + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	body, _ := io.ReadAll(r.Body)
	req, _ := http.NewRequest(r.Method, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Inject LLM config from settings into proxied request headers
	llmCfg := h.GetLLMConfig()
	if llmCfg.APIKey != "" {
		req.Header.Set("X-LLM-API-Key", llmCfg.APIKey)
		req.Header.Set("X-LLM-Model", llmCfg.Model)
		req.Header.Set("X-LLM-Base-URL", llmCfg.BaseURL)
		req.Header.Set("X-LLM-Provider", llmCfg.Provider)
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
	io.Copy(w, resp.Body)
}
