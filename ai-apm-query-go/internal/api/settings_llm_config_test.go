package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetLLMAdminConfigReturnsNonSecretConfiguration(t *testing.T) {
	settingsMu.Lock()
	previous := settings.LLM
	settings.LLM = LLMSettings{
		Provider: "deepseek",
		Model:    "deepseek-chat",
		BaseURL:  "https://api.deepseek.com/v1", // legacy row must not be exposed
		APIKey:   "encrypted-key-material",      // legacy row must not be exposed
	}
	settingsMu.Unlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		settings.LLM = previous
		settingsMu.Unlock()
	})

	recorder := httptest.NewRecorder()
	(&Handler{}).GetLLMAdminConfig(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/settings/llm/config", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GetLLMAdminConfig() status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["provider"] != "deepseek" || response.Data["model"] != "deepseek-chat" || response.Data["base_url"] != "" {
		t.Fatalf("GetLLMAdminConfig() = %v, want persisted non-secret configuration", response.Data)
	}
	if _, ok := response.Data["api_key"]; ok {
		t.Fatalf("GetLLMAdminConfig() exposed api_key: %v", response.Data)
	}
	if _, ok := response.Data["api_key_masked"]; ok {
		t.Fatalf("GetLLMAdminConfig() exposed legacy masked-key field: %v", response.Data)
	}
}
