package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─── platform_settings（KV 配置） ─────────────────────────────

// SettingDAO 平台设置（KV）数据访问。
type SettingDAO struct{}

// Get 读取一个配置项；不存在返回 ("", nil)。
func (d *SettingDAO) Get(key string) (string, error) {
	conn := GetDB()
	if conn == nil {
		return "", errors.New("mysql unavailable")
	}
	var v string
	err := conn.QueryRow("SELECT value FROM platform_settings WHERE config_key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// Set 写入（upsert）一个配置项。
func (d *SettingDAO) Set(key, value string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		"INSERT INTO platform_settings (config_key, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value=VALUES(value)",
		key, value)
	return err
}

// ─── llm_providers ───────────────────────────────────────────

// LLMProvider 数据库实体。
type LLMProvider struct {
	ID              int64
	Name            string
	Type            string
	BaseURL         string
	DefaultModel    string
	Cost            string
	Available       bool
	Enabled         bool
	APIKeyHash      string
	APIKeyEncrypted string
	CreatedAt       time.Time
}

// LLMProviderDAO LLM Provider 数据访问。
type LLMProviderDAO struct{}

// List 返回所有 provider。
func (d *LLMProviderDAO) List() ([]LLMProvider, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		"SELECT id, name, type, base_url, default_model, cost, available, enabled, api_key_hash, api_key_encrypted, created_at FROM llm_providers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LLMProvider{}
	for rows.Next() {
		var p LLMProvider
		var av, en int
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.DefaultModel,
			&p.Cost, &av, &en, &p.APIKeyHash, &p.APIKeyEncrypted, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Available = av == 1
		p.Enabled = en == 1
		out = append(out, p)
	}
	return out, nil
}

// Create 新增 provider，返回新 ID。
func (d *LLMProviderDAO) Create(p *LLMProvider) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		"INSERT INTO llm_providers (name, type, base_url, default_model, cost, available, enabled, api_key_hash, api_key_encrypted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.Name, p.Type, p.BaseURL, p.DefaultModel, p.Cost, boolToInt(p.Available), boolToInt(p.Enabled), p.APIKeyHash, p.APIKeyEncrypted)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get 按 ID 查 provider。
func (d *LLMProviderDAO) Get(id int64) (*LLMProvider, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, name, type, base_url, default_model, cost, available, enabled, api_key_hash, api_key_encrypted, created_at FROM llm_providers WHERE id=?",
		id)
	var p LLMProvider
	var av, en int
	if err := row.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.DefaultModel,
		&p.Cost, &av, &en, &p.APIKeyHash, &p.APIKeyEncrypted, &p.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	p.Available = av == 1
	p.Enabled = en == 1
	return &p, nil
}

// Update 更新 provider 的可编辑字段（name/base_url/default_model/cost/apiKeyEncrypted/apiKeyHash）。
func (d *LLMProviderDAO) Update(id int64, name, baseURL, model, cost, apiKeyHash, apiKeyEncrypted string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		"UPDATE llm_providers SET name=?, base_url=?, default_model=?, cost=?, api_key_hash=?, api_key_encrypted=? WHERE id=?",
		name, baseURL, model, cost, apiKeyHash, apiKeyEncrypted, id)
	return err
}

// Enable 设为唯一启用的 provider（先全部禁用，再启用指定）。
func (d *LLMProviderDAO) Enable(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if _, err := conn.Exec("UPDATE llm_providers SET enabled=0 WHERE enabled=1"); err != nil {
		return err
	}
	_, err := conn.Exec("UPDATE llm_providers SET enabled=1 WHERE id=?", id)
	return err
}

// Delete 删除 provider。
func (d *LLMProviderDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM llm_providers WHERE id=?", id)
	return err
}

// ─── llm_config_history ──────────────────────────────────────

// LLMConfigHistory 数据库实体。
type LLMConfigHistory struct {
	Version    int64
	Provider   string
	Model      string
	BaseURL    string
	APIKeyHash string
	Operator   string
	Comment    string
	CreatedAt  time.Time
}

// LLMConfigHistoryDAO LLM 配置历史数据访问。
type LLMConfigHistoryDAO struct{}

// NextVersion 返回指定 provider 的当前最大版本 + 1。
func (d *LLMConfigHistoryDAO) NextVersion(provider string) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	var max int64
	err := conn.QueryRow("SELECT COALESCE(MAX(version),0) FROM llm_config_history WHERE provider=?", provider).Scan(&max)
	if err != nil {
		return 0, err
	}
	return max + 1, nil
}

// Append 追加一条历史记录。
func (d *LLMConfigHistoryDAO) Append(provider, model, baseURL, apiKeyHash, operator, comment string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	ver, err := d.NextVersion(provider)
	if err != nil {
		return err
	}
	_, err = conn.Exec(
		"INSERT INTO llm_config_history (version, provider, model, base_url, api_key_hash, operator, comment) VALUES (?, ?, ?, ?, ?, ?, ?)",
		ver, provider, model, baseURL, apiKeyHash, operator, comment)
	return err
}

// List 返回历史记录（版本倒序）。
func (d *LLMConfigHistoryDAO) List(limit int) ([]LLMConfigHistory, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := conn.Query(
		"SELECT version, provider, model, base_url, api_key_hash, operator, comment, created_at FROM llm_config_history ORDER BY version DESC LIMIT ?",
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LLMConfigHistory{}
	for rows.Next() {
		var h LLMConfigHistory
		if err := rows.Scan(&h.Version, &h.Provider, &h.Model, &h.BaseURL,
			&h.APIKeyHash, &h.Operator, &h.Comment, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// GetVersion 按版本查一条历史记录。
func (d *LLMConfigHistoryDAO) GetVersion(ver int64) (*LLMConfigHistory, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT version, provider, model, base_url, api_key_hash, operator, comment, created_at FROM llm_config_history WHERE version=?",
		ver)
	var h LLMConfigHistory
	if err := row.Scan(&h.Version, &h.Provider, &h.Model, &h.BaseURL,
		&h.APIKeyHash, &h.Operator, &h.Comment, &h.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
