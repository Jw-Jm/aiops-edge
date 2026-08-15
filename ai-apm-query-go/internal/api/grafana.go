package api

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GrafanaConfig 是 query-api → deepflow grafana 代理的配置。
// RootURL 为 grafana 基础地址；APIToken 可选，配置后对上游请求注入
// `Authorization: Bearer <token>`；TLSInsecure 为 true 时跳过上游 TLS 证书校验
// （deepflow grafana 为集群内服务，常见自签证书，默认开启）。
type GrafanaConfig struct {
	RootURL     string
	APIToken    string
	TLSInsecure bool
}

// defaultGrafanaRootURL 与 nginx /grafana/ 代理目标一致（deepflow release 的 grafana）。
const defaultGrafanaRootURL = "http://deepflow-grafana.deepflow.svc.cluster.local"

// GrafanaConfigFromEnv 从环境变量读取代理配置，跟随仓库现有 env 注入风格：
//   - GRAFANA_ROOT_URL      默认 http://deepflow-grafana.deepflow.svc.cluster.local
//   - GRAFANA_API_TOKEN     可选，空则不注入 Authorization
//   - GRAFANA_TLS_INSECURE  默认 true；显式设 "false"/"0" 关闭
func GrafanaConfigFromEnv() GrafanaConfig {
	insecure := true
	if v := os.Getenv("GRAFANA_TLS_INSECURE"); v != "" {
		insecure = !strings.EqualFold(v, "false") && v != "0"
	}
	return GrafanaConfig{
		RootURL:     firstNonEmpty(os.Getenv("GRAFANA_ROOT_URL"), defaultGrafanaRootURL),
		APIToken:    os.Getenv("GRAFANA_API_TOKEN"),
		TLSInsecure: insecure,
	}
}

// grafanaHandler 持有 GrafanaConfig 与专用 http.Client（TLSInsecure 时 InsecureSkipVerify）。
type grafanaHandler struct {
	cfg    GrafanaConfig
	client *http.Client
}

// NewGrafanaHandler 组装 grafana 代理 handler（配置注入点，由 server 组装处调用）。
func NewGrafanaHandler(cfg GrafanaConfig) *grafanaHandler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.TLSInsecure {
		// 内网 deepflow grafana 通常使用自签证书，经 GRAFANA_TLS_INSECURE 显式开启跳过校验；
		// 与 data_sync.go 的 K8S_INSECURE_SKIP_VERIFY 处理方式一致。
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- 显式配置的内网信任
	}
	return &grafanaHandler{
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}
}

// maxGrafanaPayload 限制 grafana 代理响应体上限（20MB），防止超大 dashboard JSON 占内存。
const maxGrafanaPayload = 20 << 20

// proxy 是三个端点的公共转发逻辑：
//   - 上游连接失败 → 502（grafana 不可达）
//   - 上游 4xx/5xx → 状态码与响应体原样透传
//   - rawQuery 非空时原样透传到上游（search 的 query 参数等）
func (h *grafanaHandler) proxy(w http.ResponseWriter, r *http.Request, upstreamPath, rawQuery string) {
	if h.cfg.RootURL == "" {
		respondError(w, http.StatusServiceUnavailable, "grafana not configured")
		return
	}
	target := strings.TrimRight(h.cfg.RootURL, "/") + upstreamPath
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.APIToken)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("grafana proxy error: %v", err)
		respondError(w, http.StatusBadGateway, "grafana 不可达")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGrafanaPayload+1))
	if err != nil {
		respondError(w, http.StatusBadGateway, "grafana 读取失败")
		return
	}
	if len(body) > maxGrafanaPayload {
		respondError(w, http.StatusBadGateway, "grafana 响应过大(>20MB)")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// Search 处理 GET /api/v1/grafana/search?query=，转发上游 /api/search（dashboard 搜索）。
func (h *grafanaHandler) Search(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/api/search", r.URL.RawQuery)
}

// Health 处理 GET /api/v1/grafana/health，转发上游 /api/health。
func (h *grafanaHandler) Health(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/api/health", "")
}

// ProxyDashboard 处理 GET /api/v1/grafana/dashboards/{uid}，转发上游 /api/dashboards/uid/{uid}。
func (h *grafanaHandler) ProxyDashboard(w http.ResponseWriter, r *http.Request) {
	uid := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/grafana/dashboards/"), "/")
	if uid == "" {
		respondError(w, http.StatusBadRequest, "dashboard uid required")
		return
	}
	h.proxy(w, r, "/api/dashboards/uid/"+url.PathEscape(uid), "")
}
