package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// LLMProvider 响应实体。
type LLMProvider struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
	Cost         string `json:"cost"`
	Available    bool   `json:"available"`
	Enabled      bool   `json:"enabled"`
	APIKeyMasked string `json:"api_key_masked"`
	CreatedAt    string `json:"created_at"`
}

func (h *Handler) ListLLMProviders(w http.ResponseWriter, r *http.Request) {
	d := &store.LLMProviderDAO{}
	list, err := d.List()
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	providers := []LLMProvider{}
	for _, p := range list {
		providers = append(providers, LLMProvider{
			ID:           int(p.ID),
			Name:         p.Name,
			Type:         p.Type,
			BaseURL:      p.BaseURL,
			DefaultModel: p.DefaultModel,
			Cost:         p.Cost,
			Available:    p.Available,
			Enabled:      p.Enabled,
			APIKeyMasked: "sk-***",
			CreatedAt:    p.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	respondJSON(w, 200, map[string]interface{}{"providers": providers, "total": len(providers)})
}

func (h *Handler) CreateLLMProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, 405, map[string]interface{}{"error": "method not allowed"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	var p map[string]interface{}
	if json.Unmarshal(body, &p) != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid JSON"})
		return
	}

	name, _ := p["name"].(string)
	baseURL, _ := p["base_url"].(string)
	model, _ := p["default_model"].(string)
	typ, _ := p["type"].(string)
	cost, _ := p["cost"].(string)
	apiKey, _ := p["api_key"].(string)

	if name == "" || baseURL == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "name and base_url required"})
		return
	}
	if typ == "" {
		typ = "openai_compatible"
	}
	if cost == "" {
		cost = "人民币"
	}
	if model == "" {
		model = "default"
	}

	apiKeyHash := "****"
	apiKeyEncrypted := ""
	if apiKey != "" {
		apiKeyHash = "sha256:" + sha256Hash(apiKey)[:16]
		apiKeyEncrypted = encryptAPIKey(apiKey)
	}

	d := &store.LLMProviderDAO{}
	id, err := d.Create(&store.LLMProvider{
		Name: name, Type: typ, BaseURL: baseURL, DefaultModel: model,
		Cost: cost, Available: true, Enabled: false,
		APIKeyHash: apiKeyHash, APIKeyEncrypted: apiKeyEncrypted,
	})
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 201, map[string]interface{}{"id": id, "message": "created"})
}

func (h *Handler) UpdateLLMProvider(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/providers/"), "/")[0]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid id"})
		return
	}

	body, _ := io.ReadAll(r.Body)
	var p map[string]interface{}
	if json.Unmarshal(body, &p) != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid JSON"})
		return
	}

	d := &store.LLMProviderDAO{}
	existing, err := d.Get(id)
	if err != nil || existing == nil {
		respondJSON(w, 404, map[string]interface{}{"error": "provider not found"})
		return
	}

	name, baseURL, model, cost := existing.Name, existing.BaseURL, existing.DefaultModel, existing.Cost
	apiKeyHash, apiKeyEnc := existing.APIKeyHash, existing.APIKeyEncrypted
	if v, ok := p["name"].(string); ok && v != "" {
		name = v
	}
	if v, ok := p["base_url"].(string); ok && v != "" {
		baseURL = v
	}
	if v, ok := p["default_model"].(string); ok && v != "" {
		model = v
	}
	if v, ok := p["cost"].(string); ok && v != "" {
		cost = v
	}
	if v, ok := p["api_key"].(string); ok && v != "" {
		apiKeyHash = "sha256:" + sha256Hash(v)[:16]
		apiKeyEnc = encryptAPIKey(v)
	}

	if err := d.Update(id, name, baseURL, model, cost, apiKeyHash, apiKeyEnc); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]interface{}{"message": "updated"})
}

func (h *Handler) DeleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/providers/"), "/")[0]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid id"})
		return
	}
	d := &store.LLMProviderDAO{}
	_ = d.Delete(id)
	respondJSON(w, 200, map[string]interface{}{"message": "deleted"})
}

func (h *Handler) EnableLLMProvider(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/providers/"), "/")
	if len(parts) < 2 || parts[1] != "enable" {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid path"})
		return
	}
	idStr := parts[0]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid id"})
		return
	}

	d := &store.LLMProviderDAO{}
	p, err := d.Get(id)
	if err != nil || p == nil {
		respondJSON(w, 404, map[string]interface{}{"error": "provider not found"})
		return
	}

	// 禁用所有, 启用当前
	if err := d.Enable(id); err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}

	// 同步到 settings，并同步该 provider 的真实 API key
	newKey := ""
	if p.APIKeyEncrypted != "" {
		newKey = decryptAPIKey(p.APIKeyEncrypted)
	}
	settingsMu.Lock()
	settings.LLM.Provider = p.Name
	settings.LLM.Model = p.DefaultModel
	settings.LLM.BaseURL = p.BaseURL
	if newKey != "" {
		settings.LLM.APIKey = encryptAPIKey(newKey)
	}
	settingsMu.Unlock()
	saveSettings(settings)

	respondJSON(w, 200, map[string]interface{}{"message": "enabled", "provider": p.Name})
}

// getProviderName 返回指定 provider 的名称（用于连接测试显示）
func getProviderName(providerID string) string {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return ""
	}
	d := &store.LLMProviderDAO{}
	p, err := d.Get(id)
	if err != nil || p == nil {
		return ""
	}
	return p.Name
}

// getProviderEncryptedKey 读取指定 provider 的加密 API Key（用于连接测试），返回解密后的真实 key。
func getProviderEncryptedKey(providerID string) string {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return ""
	}
	d := &store.LLMProviderDAO{}
	p, err := d.Get(id)
	if err != nil || p == nil || p.APIKeyEncrypted == "" {
		return ""
	}
	return decryptAPIKey(p.APIKeyEncrypted)
}
