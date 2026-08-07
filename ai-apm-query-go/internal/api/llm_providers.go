package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

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

func chQueryClickhouse(chHost, chPort, sql string) (string, error) {
	resp, err := http.Post("http://"+chHost+":"+chPort+"/", "text/plain", strings.NewReader(sql))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)), nil
}

func chGetClickhouse(chHost, chPort, sql string) (string, error) {
	resp, err := http.Get("http://" + chHost + ":" + chPort + "/?query=" + sql)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)), nil
}

func (h *Handler) ListLLMProviders(w http.ResponseWriter, r *http.Request) {
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }

	data, err := chGetClickhouse(chHost, chPort,
		"SELECT+id,name,type,base_url,default_model,cost,available,enabled,toString(created_at)+FROM+observability.llm_providers+ORDER+BY+id")
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}

	providers := []LLMProvider{}
	if data != "" {
		for _, line := range strings.Split(data, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) < 9 { continue }
			id, _ := strconv.Atoi(fields[0])
			available, _ := strconv.Atoi(fields[6])
			enabled, _ := strconv.Atoi(fields[7])
			providers = append(providers, LLMProvider{
				ID: id, Name: fields[1], Type: fields[2], BaseURL: fields[3],
				DefaultModel: fields[4], Cost: fields[5],
				Available: available == 1, Enabled: enabled == 1,
				APIKeyMasked: "sk-***",
				CreatedAt: fields[8],
			})
		}
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

	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }
	ensureProviderEncryptedColumn(chHost, chPort)

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
	if typ == "" { typ = "openai_compatible" }
	if cost == "" { cost = "人民币" }
	if model == "" { model = "default" }

	apiKeyHash := "****"
	apiKeyEncrypted := ""
	if apiKey != "" {
		apiKeyHash = "sha256:" + sha256Hash(apiKey)[:16]
		// 同时加密存储真实 key，供连接测试时解密
		apiKeyEncrypted = encryptAPIKey(apiKey)
	}

	maxQ, _ := chGetClickhouse(chHost, chPort, "SELECT+coalesce(max(id),0)+FROM+observability.llm_providers")
	newID := 1
	if maxQ != "" {
		n, _ := strconv.Atoi(maxQ)
		newID = n + 1
	}

	safeName := strings.ReplaceAll(name, "'", "\\'")
	safeURL := strings.ReplaceAll(baseURL, "'", "\\'")
	safeModel := strings.ReplaceAll(model, "'", "\\'")
	safeType := strings.ReplaceAll(typ, "'", "\\'")
	safeCost := strings.ReplaceAll(cost, "'", "\\'")
	safeKey := strings.ReplaceAll(apiKeyEncrypted, "'", "\\'")
	now := time.Now().Format("2006-01-02 15:04:05")

	sql := "INSERT INTO observability.llm_providers (id, name, type, base_url, default_model, cost, available, enabled, api_key_hash, api_key_encrypted, created_at) VALUES " +
		"(" + strconv.Itoa(newID) + ", '" + safeName + "', '" + safeType + "', '" + safeURL + "', '" + safeModel + "', '" + safeCost + "', 1, 0, '" + apiKeyHash + "', '" + safeKey + "', '" + now + "')"
	_, err := chQueryClickhouse(chHost, chPort, sql)
	if err != nil {
		respondJSON(w, 500, map[string]interface{}{"error": err.Error()})
		return
	}

	respondJSON(w, 201, map[string]interface{}{"id": newID, "message": "created"})
}

func (h *Handler) UpdateLLMProvider(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/providers/"), "/")[0]
	if _, err := strconv.Atoi(idStr); err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid id"})
		return
	}

	body, _ := io.ReadAll(r.Body)
	var p map[string]interface{}
	if json.Unmarshal(body, &p) != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid JSON"})
		return
	}

	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }

	updates := []string{}
	if v, ok := p["name"].(string); ok && v != "" {
		updates = append(updates, "name='"+strings.ReplaceAll(v, "'", "\\'")+"'")
	}
	if v, ok := p["base_url"].(string); ok && v != "" {
		updates = append(updates, "base_url='"+strings.ReplaceAll(v, "'", "\\'")+"'")
	}
	if v, ok := p["default_model"].(string); ok && v != "" {
		updates = append(updates, "default_model='"+strings.ReplaceAll(v, "'", "\\'")+"'")
	}
	if v, ok := p["cost"].(string); ok && v != "" {
		updates = append(updates, "cost='"+strings.ReplaceAll(v, "'", "\\'")+"'")
	}
	if v, ok := p["api_key"].(string); ok && v != "" {
		updates = append(updates, "api_key_hash='sha256:"+sha256Hash(v)[:16]+"'")
		enc := strings.ReplaceAll(encryptAPIKey(v), "'", "\\'")
		updates = append(updates, "api_key_encrypted='"+enc+"'")
	}
	if len(updates) > 0 {
		sql := "ALTER TABLE observability.llm_providers UPDATE " + strings.Join(updates, ", ") + " WHERE id=" + idStr
		chQueryClickhouse(chHost, chPort, sql)
	}
	respondJSON(w, 200, map[string]interface{}{"message": "updated"})
}

func (h *Handler) DeleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/providers/"), "/")[0]
	if _, err := strconv.Atoi(idStr); err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid id"})
		return
	}

	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }
	sql := "ALTER TABLE observability.llm_providers DELETE WHERE id=" + idStr
	chQueryClickhouse(chHost, chPort, sql)
	respondJSON(w, 200, map[string]interface{}{"message": "deleted"})
}

func (h *Handler) EnableLLMProvider(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/llm/providers/"), "/")
	if len(parts) < 2 || parts[1] != "enable" {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid path"})
		return
	}
	idStr := parts[0]
	if _, err := strconv.Atoi(idStr); err != nil {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid id"})
		return
	}

	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" { chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local" }
	if chPort == "" { chPort = "8123" }

	row, _ := chGetClickhouse(chHost, chPort,
		"SELECT+name,base_url,default_model+FROM+observability.llm_providers+WHERE+id%3D"+idStr+"+LIMIT+1")
	if row == "" {
		respondJSON(w, 404, map[string]interface{}{"error": "provider not found"})
		return
	}
	fields := strings.Split(row, "\t")
	if len(fields) < 3 {
		respondJSON(w, 500, map[string]interface{}{"error": "invalid record"})
		return
	}

	// 禁用所有, 启用当前
	chQueryClickhouse(chHost, chPort, "ALTER TABLE observability.llm_providers UPDATE enabled=0 WHERE enabled=1")
	chQueryClickhouse(chHost, chPort, "ALTER TABLE observability.llm_providers UPDATE enabled=1 WHERE id="+idStr)

	// 同步到 settings，并同步该 provider 的真实 API key（修复：启用后仍用旧 provider key 的问题）
	newKey := getProviderEncryptedKey(idStr)
	settingsMu.Lock()
	settings.LLM.Provider = fields[0]
	settings.LLM.Model = fields[2]
	settings.LLM.BaseURL = fields[1]
	if newKey != "" {
		settings.LLM.APIKey = encryptAPIKey(newKey)
	}
	settingsMu.Unlock()
	saveSettings(settings)

	respondJSON(w, 200, map[string]interface{}{"message": "enabled", "provider": fields[0]})
}

// ensureProviderEncryptedColumn 确保 llm_providers 表存在 api_key_encrypted 列（幂等）
func ensureProviderEncryptedColumn(chHost, chPort string) {
	chQueryClickhouse(chHost, chPort,
		"ALTER TABLE observability.llm_providers ADD COLUMN IF NOT EXISTS api_key_encrypted String DEFAULT ''")
}

// getProviderName 返回指定 provider 的名称（用于连接测试显示）
func getProviderName(providerID string) string {
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" {
		chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local"
	}
	if chPort == "" {
		chPort = "8123"
	}
	row, _ := chGetClickhouse(chHost, chPort,
		"SELECT+name+FROM+observability.llm_providers+WHERE+id%3D"+providerID+"+LIMIT+1")
	if row == "" {
		return ""
	}
	return strings.TrimSpace(row)
}

// getProviderEncryptedKey 读取指定 provider 的加密 API Key（用于连接测试），返回解密后的真实 key。
func getProviderEncryptedKey(providerID string) string {
	chHost := os.Getenv("CLICKHOUSE_HOST")
	chPort := os.Getenv("CLICKHOUSE_PORT")
	if chHost == "" {
		chHost = "clickhouse-0.clickhouse.observability.svc.cluster.local"
	}
	if chPort == "" {
		chPort = "8123"
	}
	row, _ := chGetClickhouse(chHost, chPort,
		"SELECT+api_key_encrypted+FROM+observability.llm_providers+WHERE+id%3D"+providerID+"+LIMIT+1")
	if row == "" {
		return ""
	}
	// 列可能为空（旧数据无加密 key）
	if strings.HasPrefix(row, "\\N") || strings.HasPrefix(row, "NULL") || strings.TrimSpace(row) == "" {
		return ""
	}
	return decryptAPIKey(strings.TrimSpace(row))
}
