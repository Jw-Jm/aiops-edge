package api

import "net/http"

// RegisterGrafanaRoutes 注册 Grafana 代理端点（工作流 F1）：
//   - GET /api/v1/grafana/health          → 上游 /api/health
//   - GET /api/v1/grafana/search?query=   → 上游 /api/search
//   - GET /api/v1/grafana/dashboards/{uid} → 上游 /api/dashboards/uid/{uid}
//
// 跟随仓库现有路由注册方式（cmd/api/main.go 中 http.ServeMux + HandleFunc）。
// 这三个端点经 AuthMiddleware 统一 JWT 鉴权（与 /api/v1/deepflow/status 同级）。
func RegisterGrafanaRoutes(mux *http.ServeMux, gh *grafanaHandler) {
	mux.HandleFunc("/api/v1/grafana/health", gh.Health)
	mux.HandleFunc("/api/v1/grafana/search", gh.Search)
	mux.HandleFunc("/api/v1/grafana/dashboards/", gh.ProxyDashboard)
}
