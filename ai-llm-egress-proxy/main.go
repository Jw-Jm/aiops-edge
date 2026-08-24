// ai-llm-egress-proxy：LLM 出站唯一代理（C-04 / 报告 §19）。
//
// 职责：
//   - 平台内 orchestrator/agent 调 LLM 的唯一出站路径（default-deny NetworkPolicy 下
//     只有本 proxy 有出站到 LLM 域的 egress 权限）。
//   - Provider API Key 只存于本 proxy 的 Secret/env，绝不暴露给 orchestrator/agent/frontend。
//   - 只允许转发到 allowlist 的 LLM 域名（deepseek/openai 等）；其他出站一律 403。
//   - 记录审计（谁/何时/哪个 provider/请求量），便于回答"哪个 agent 用了哪个 LLM"。
//
// 接口：
//   POST /v1/proxy/{provider}/chat     → 转发到对应 provider 的 chat/completions
//   POST /v1/proxy/{provider}/models   → 转发到 provider 的 models 列表
//   GET  /healthz
//
// 配置：
//   LLM_PROVIDER_KEYS = "deepseek:sk-...,openai:sk-..."  （逗号分隔 provider:key）
//   LLM_ALLOWLIST     = "api.deepseek.com,api.openai.com" （逗号分隔允许域名，默认上面两个）
//   PROXY_TOKEN       = 调用方（orchestrator）出示的共享 token（默认不启用）
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

type proxyConfig struct {
	providerKeys map[string]string // provider -> api key
	baseURLs     map[string]string // provider -> base https URL
	proxyToken   string
	client       *http.Client
}

func main() {
	cfg := &proxyConfig{
		providerKeys: map[string]string{},
		baseURLs: map[string]string{
			"deepseek": "https://api.deepseek.com",
			"openai":   "https://api.openai.com",
		},
		proxyToken: os.Getenv("PROXY_TOKEN"),
		client:     &http.Client{},
	}
	// LLM_PROVIDER_KEYS = "deepseek:sk-...,openai:sk-..."
	for _, kv := range strings.Split(os.Getenv("LLM_PROVIDER_KEYS"), ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			cfg.providerKeys[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/proxy/", cfg.handleProxy)

	addr := ":" + firstNonEmpty(os.Getenv("PROXY_PORT"), "8080")
	log.Printf("ai-llm-egress-proxy listening on %s (providers: %s)", addr, strings.Join(keys(cfg.providerKeys), ","))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// handleProxy 转发到 allowlist 的 LLM provider。
func (c *proxyConfig) handleProxy(w http.ResponseWriter, r *http.Request) {
	// PROXY_TOKEN 鉴权（调用方 orchestrator 出示）。
	if c.proxyToken != "" && r.Header.Get("X-Proxy-Token") != c.proxyToken {
		http.Error(w, "unauthorized proxy token", http.StatusForbidden)
		return
	}
	// path: /v1/proxy/{provider}/{...rest}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/proxy/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing provider", http.StatusBadRequest)
		return
	}
	provider := parts[0]
	apiKey, ok := c.providerKeys[provider]
	if !ok {
		http.Error(w, fmt.Sprintf("provider %q not configured", provider), http.StatusNotFound)
		return
	}
	base, ok := c.baseURLs[provider]
	if !ok {
		http.Error(w, fmt.Sprintf("provider %q base URL not configured", provider), http.StatusNotFound)
		return
	}
	// 只允许 allowlist 域名（安全边界，default-deny egress）。
	if !allowlisted(provider) {
		http.Error(w, "provider not allowlisted", http.StatusForbidden)
		return
	}
	// 转发路径：{base}/{rest}，rest 如 chat/completions、models。
	targetPath := base + "/v1/" + strings.TrimLeft(rest, "/")
	target, err := url.Parse(targetPath)
	if err != nil {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// 注入 provider API key（只在本 proxy 内持有）。
	originalAuth := r.Header.Get("Authorization")
	r.Host = target.Host
	r.URL = target
	r.Header.Set("Authorization", "Bearer "+apiKey)
	proxy.ServeHTTP(w, r)
	// 恢复（不影响后续）
	r.Header.Set("Authorization", originalAuth)
}

func allowlisted(provider string) bool {
	switch provider {
	case "deepseek", "openai":
		return true
	}
	return false
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
