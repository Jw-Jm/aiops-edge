package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/api"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// randomPassword 用 crypto/rand 生成 n 字节的十六进制随机密码（n*2 个字符）。
func randomPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "admin-" + hex.EncodeToString([]byte(fmt.Sprintf("%d", n)))
	}
	return hex.EncodeToString(b)
}

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	chHost := flag.String("ch-host", "clickhouse.observability.svc.cluster.local", "ClickHouse host")
	chPort := flag.Int("ch-port", 8123, "ClickHouse HTTP port")
	flag.Parse()

	if h := os.Getenv("CLICKHOUSE_HOST"); h != "" {
		*chHost = h
	}
	if p := os.Getenv("CLICKHOUSE_PORT"); p != "" {
		fmt.Sscanf(p, "%d", chPort)
	}

	// MySQL：先建表/迁移（EnsureSchema），再初始化 handler 加载规则，
	// 确保 alert_rules 的新列（baseline_seconds 等）在 loadAlertRules 前已存在
	store.EnsureSchema()

	handler := api.NewHandler(*chHost, *chPort)
	if vmURL := os.Getenv("VICTORIA_METRICS_URL"); vmURL != "" {
		handler.SetVMURL(vmURL)
	}
	// 注入 CH 连接供告警事件持久化（必须先于评估引擎启动）
	api.SetAlertCH(handler)
	handler.StartAlertEvaluation()
	if db := store.GetDB(); db != nil {
		// admin 初始密码从环境变量注入（生产必设）；未设置时生成随机强密码并打印一次性提示。
		adminPW := os.Getenv("ADMIN_INITIAL_PASSWORD")
		if adminPW == "" {
			adminPW = randomPassword(16)
			log.Printf("ADMIN_INITIAL_PASSWORD not set: generated random admin password (first login / reset only): %s", adminPW)
		}
		if adminHash, err := bcrypt.GenerateFromPassword([]byte(adminPW), bcrypt.DefaultCost); err == nil {
			_ = (&store.UserDAO{}).SeedAdmin(string(adminHash))
		}
		// 拓扑类型目录内置种子（幂等）
		_ = store.SeedTopologyTypes()
	}
	api.InitK8sRules()

	mux := http.NewServeMux()

	// Auth routes (no auth required)
	mux.HandleFunc("/api/v1/auth/login", handler.Login)
	mux.HandleFunc("/api/v1/login", handler.Login)

	// User management (admin)
	mux.HandleFunc("/api/v1/users", handler.RequireRole("admin", handler.UserRouter))
	mux.HandleFunc("/api/v1/users/", handler.RequireRole("admin", handler.UserRouter))
	mux.HandleFunc("/api/v1/me", handler.Me)

	// Service catalog (read any, write admin)
	mux.HandleFunc("/api/v1/catalog/services", handler.RequireRoleForWrite("admin", handler.CatalogRouter))
	mux.HandleFunc("/api/v1/catalog/services/", handler.RequireRoleForWrite("admin", handler.CatalogRouter))

	// Devices (read any, write admin)
	mux.HandleFunc("/api/v1/devices", handler.RequireRoleForWrite("admin", handler.DeviceRouter))
	mux.HandleFunc("/api/v1/devices/", handler.RequireRoleForWrite("admin", handler.DeviceRouter))

	// Clusters (read any, write/sync admin)
	mux.HandleFunc("/api/v1/clusters", handler.RequireRoleForWrite("admin", handler.ClusterRouter))
	mux.HandleFunc("/api/v1/clusters/", handler.RequireRoleForWrite("admin", handler.ClusterRouter))

	// Health (no auth required)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Services
	mux.HandleFunc("/api/v1/services", handler.ListServices)
	mux.HandleFunc("/api/v1/services/", handler.ServiceDetail)
	// Traces
	mux.HandleFunc("/api/v1/traces", handler.ListTraces)
	mux.HandleFunc("/api/v1/traces/", handler.TraceRouter)
	// Metrics & Topology & Logs
	mux.HandleFunc("/api/v1/metrics/query", handler.QueryMetrics)
	mux.HandleFunc("/api/v1/metrics/query_range", handler.QueryRange)
	mux.HandleFunc("/api/v1/capacity/forecast", handler.CapacityForecast)
	mux.HandleFunc("/api/v1/capacity/instances", handler.CapacityInstances)
	mux.HandleFunc("/api/v1/dashboard/stats", handler.DashboardStats)
	mux.HandleFunc("/api/v1/dashboard/resources", handler.DashboardResources)
	mux.HandleFunc("/api/v1/topology/global", handler.GlobalTopology)
	mux.HandleFunc("/api/v1/topology/node/", handler.TopologyNodeDetail)
	mux.HandleFunc("/api/v1/topology/sync", handler.SyncTopologyFromK8s)
	mux.HandleFunc("/api/v1/topology/sync-catalog", handler.SyncTopologyCatalog)
	// Topology graph catalogue (typed property graph)
	mux.HandleFunc("/api/v1/topology/nodes", handler.TopologyNodesRouter)
	mux.HandleFunc("/api/v1/topology/nodes/", handler.TopologyNodesRouter)
	mux.HandleFunc("/api/v1/topology/relations", handler.TopologyRelationsRouter)
	mux.HandleFunc("/api/v1/topology/relations/", handler.TopologyRelationsRouter)
	mux.HandleFunc("/api/v1/topology/node-types", handler.TopologyNodeTypesRouter)
	mux.HandleFunc("/api/v1/topology/node-types/", handler.TopologyNodeTypesRouter)
	mux.HandleFunc("/api/v1/topology/relation-types", handler.TopologyRelationTypesRouter)
	mux.HandleFunc("/api/v1/topology/relation-types/", handler.TopologyRelationTypesRouter)
	mux.HandleFunc("/api/v1/data/sync", handler.SyncDataFromK8s)
	mux.HandleFunc("/api/v1/logs/query", handler.QueryLogs)
	mux.HandleFunc("/api/v1/logs/aggregate", handler.LogAggregate)
	mux.HandleFunc("/api/v1/logs/victorialogs", handler.ProxyVictoriaLogs)
	// Infrastructure (K8s)
	mux.HandleFunc("/api/v1/infrastructure/nodes", handler.Nodes)
	mux.HandleFunc("/api/v1/infrastructure/pods", handler.Pods)
	mux.HandleFunc("/api/v1/infrastructure/pods/", handler.PodDetail)
	mux.HandleFunc("/api/v1/infrastructure/deployments", handler.Deployments)
	mux.HandleFunc("/api/v1/infrastructure/namespaces", handler.Namespaces)
	mux.HandleFunc("/api/v1/nodes/metrics", handler.NodesMetrics)
	// Infrastructure (K8s + KubeVirt, 5.2/5.3)
	mux.HandleFunc("/api/v1/infrastructure/hpa", handler.HPA)
	mux.HandleFunc("/api/v1/infrastructure/vms", handler.VMs)
	mux.HandleFunc("/api/v1/infrastructure/vms/", handler.VMDetail)
	// Settings (LLM + K8s)
	// 安全(P0-4)：LLM 配置的写操作与敏感子路径一律要求 admin 角色，防止普通登录用户
	// 篡改 base_url 窃取/回传已保存的 API key（GET /settings/llm 保持登录可读，
	// 仅返回脱敏配置供前端判断"已配置"状态）。
	mux.HandleFunc("/api/v1/settings/llm", handler.RequireRoleForWrite("admin", handler.SettingsLLM))
	mux.HandleFunc("/api/v1/settings/llm/internal", handler.GetInternalLLMSettings)
	mux.HandleFunc("/api/v1/settings/llm/test", handler.RequireRole("admin", handler.TestLLMConnection))
	mux.HandleFunc("/api/v1/settings/llm/models", handler.RequireRole("admin", handler.ModelsLLM))
	mux.HandleFunc("/api/v1/settings/llm/history", handler.ListLLMHistory)
	mux.HandleFunc("/api/v1/settings/llm/providers", handler.RequireRoleForWrite("admin", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.ListLLMProviders(w, r)
		case http.MethodPost:
			handler.CreateLLMProvider(w, r)
		default:
			w.WriteHeader(405)
		}
	}))
	mux.HandleFunc("/api/v1/settings/llm/providers/", handler.RequireRole("admin", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/enable") {
			handler.EnableLLMProvider(w, r)
		} else {
			switch r.Method {
			case http.MethodPut:
				handler.UpdateLLMProvider(w, r)
			case http.MethodDelete:
				handler.DeleteLLMProvider(w, r)
			default:
				w.WriteHeader(405)
			}
		}
	}))
	mux.HandleFunc("/api/v1/settings/llm/history/", handler.RequireRole("admin", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "rollback") {
			handler.RollbackLLMConfig(w, r)
		} else {
			w.WriteHeader(404)
		}
	}))
	mux.HandleFunc("/api/v1/settings/k8s", handler.GetK8sSettings)
	// DeepFlow integration
	mux.HandleFunc("/api/v1/deepflow/status", handler.DeepFlowStatus)
	// AI proxy
	mux.HandleFunc("/api/v1/ai/chat", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/sessions/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/sessions", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/session/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/shell/check", handler.ProxyAI)
	// P0-2: aichat 内嵌审批——suggestion/execute 走 ProxyAI 注入 X-Internal-Token，
	// 使 orchestrator 的 _require_approver 通过（否则 nginx 直连 orchestrator 缺 token → 403）。
	mux.HandleFunc("/api/v1/ai/suggestion/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/suggestion", handler.ProxyAI)
	// aichat 最终版本报告：代理到 ai-orchestrator（/ai/final_report，汇总会话执行历史生成报告）
	mux.HandleFunc("/api/v1/ai/final_report", handler.ProxyAI)
	// NL2SQL：代理到 ai-orchestrator（/ai/nl2sql/translate、/ai/nl2sql/{id}/execute、/ai/nl2sql/{id}）
	mux.HandleFunc("/api/v1/ai/nl2sql/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/nl2sql", handler.ProxyAI)
	// P1-4.3 修复：补齐 AI Skills / Agents / Knowledge / Rules / Flows 代理路由，
	// 此前缺失导致前端 AI 工具"技能目录"永远 404 为空（orchestrator 实际有 8 个内置 skill）。
	mux.HandleFunc("/api/v1/ai/skills/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/skills", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/agents/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/agents", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/knowledge/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/knowledge", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/rules/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/rules", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/flows/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/flows", handler.ProxyAI)
	// WebShell WebSocket：AuthMiddleware 已放行该路径（WebSocket 无 header），
	// handler 内部从 ?token= 验证 JWT 并代理到 orchestrator（注入 INTERNAL_TOKEN）。
	mux.HandleFunc("/api/v1/shell/ws", handler.ProxyShellWS)
	mux.HandleFunc("/api/v1/mcp/tools", handler.ProxyAI)
	mux.HandleFunc("/api/v1/mcp/call", handler.ProxyAI)
	// 自研工作流引擎：代理到 ai-orchestrator（/ai/workflows CRUD + run + runs + resume）
	mux.HandleFunc("/api/v1/ai/workflows/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/workflows", handler.ProxyAI)
	// Hardware / IPMI：代理到 ai-orchestrator（其上有 /ipmi/sensors、/node/health 等端点）。
	// 经 AuthMiddleware 完成 JWT 鉴权，ProxyAI 注入内部 token，避免直连 orchestrator。
	mux.HandleFunc("/api/v1/ipmi/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ipmi", handler.ProxyAI)
	mux.HandleFunc("/api/v1/node/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/node", handler.ProxyAI)
	// SNMP：代理到 ai-orchestrator（其上有 /snmp/devices、/interfaces、/collect 等端点）。
	mux.HandleFunc("/api/v1/snmp/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/snmp", handler.ProxyAI)
	// 安全：/api/v1/health 是公开健康端点，必须返回本服务自身状态，
	// 绝不能接到 ProxyAI（否则会成为未鉴权代理入口，绕到 ai-orchestrator）。
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	// Ops tasks (AIOps Agent)
	mux.HandleFunc("/api/v1/ops/", handler.ProxyAI)
	// Prometheus metrics
	mux.HandleFunc("/metrics", handler.ProxyAI)
	// Alerts
	mux.HandleFunc("/api/v1/alerts/rules", handler.AlertRules)
	mux.HandleFunc("/api/v1/alerts/rules/", handler.AlertRuleByID)
	mux.HandleFunc("/api/v1/alerts/events", handler.AlertEvents)
	mux.HandleFunc("/api/v1/alerts/events/", handler.AlertEventRouter)
	mux.HandleFunc("/api/v1/alerts/aggregation", handler.AlertAggregation)
	mux.HandleFunc("/api/v1/alerts/silences", handler.AlertSilences)
	mux.HandleFunc("/api/v1/alerts/silences/", handler.AlertSilenceByID)
	// SLO 目标（读任意，写 admin，isAdmin 在 handler 内校验）
	mux.HandleFunc("/api/v1/slo", handler.SLORouter)
	mux.HandleFunc("/api/v1/slo/", handler.SLORouterByID)
	// Dashboard Monitor 看板面板（B4）
	mux.HandleFunc("/api/v1/dashboard/panels", handler.DashboardRouter)
	mux.HandleFunc("/api/v1/dashboard/panels/", handler.DashboardRouterByID)
	// Tenants (List/Create)
	mux.HandleFunc("/api/v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.ListTenants(w, r)
		case http.MethodPost:
			handler.CreateTenant(w, r)
		default:
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/api/v1/tenants/", handler.DeleteTenant)
	// System (HPA + Cache + Redis + Components)
	mux.HandleFunc("/api/v1/system/status", handler.SystemStatus)
	mux.HandleFunc("/api/v1/system/cache", handler.CacheStats)
	mux.HandleFunc("/api/v1/system/cache/invalidate", handler.InvalidateCache)
	mux.HandleFunc("/api/v1/system/components", handler.SystemComponents)

	// Wrap: CORS → Auth
	corsHandler := api.CORSMiddleware(mux)
	authHandler := api.AuthMiddleware(corsHandler)

	server := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: authHandler}
	// Production K8s log shipper → VictoriaLogs
	go handler.StartLogShipper()

	go func() {
		log.Printf("Query API listening on :%d", *port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
	server.Close()
}
