package main

import (
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

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	chHost := flag.String("ch-host", "clickhouse-0.clickhouse.observability.svc.cluster.local", "ClickHouse host")
	chPort := flag.Int("ch-port", 8123, "ClickHouse HTTP port")
	flag.Parse()

	if h := os.Getenv("CLICKHOUSE_HOST"); h != "" {
		*chHost = h
	}
	if p := os.Getenv("CLICKHOUSE_PORT"); p != "" {
		fmt.Sscanf(p, "%d", chPort)
	}

	handler := api.NewHandler(*chHost, *chPort)
	if vmURL := os.Getenv("VICTORIA_METRICS_URL"); vmURL != "" {
		handler.SetVMURL(vmURL)
	}
	handler.StartAlertEvaluation()
	api.InitK8sRules()

	// MySQL：应用 users 表迁移 + 种子 admin（密码 admin123，失败不阻塞）
	store.EnsureSchema()
	if db := store.GetDB(); db != nil {
		if adminHash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost); err == nil {
			_ = (&store.UserDAO{}).SeedAdmin(string(adminHash))
		}
		// 拓扑类型目录内置种子（幂等）
		_ = store.SeedTopologyTypes()
	}

	mux := http.NewServeMux()

	// Auth routes (no auth required)
	mux.HandleFunc("/api/v1/auth/login", handler.Login)
	mux.HandleFunc("/api/v1/login", handler.Login)

	// User management (admin)
	mux.HandleFunc("/api/v1/users", handler.RequireRole("admin", handler.UserRouter))
	mux.HandleFunc("/api/v1/users/", handler.RequireRole("admin", handler.UserRouter))
	mux.HandleFunc("/api/v1/me", handler.Me)

	// Service catalog (read any, write admin)
	mux.HandleFunc("/api/v1/catalog/services", handler.CatalogRouter)
	mux.HandleFunc("/api/v1/catalog/services/", handler.CatalogRouter)

	// Devices (read any, write admin)
	mux.HandleFunc("/api/v1/devices", handler.DeviceRouter)
	mux.HandleFunc("/api/v1/devices/", handler.DeviceRouter)

	// Clusters (read any, write/sync admin)
	mux.HandleFunc("/api/v1/clusters", handler.ClusterRouter)
	mux.HandleFunc("/api/v1/clusters/", handler.ClusterRouter)

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
	mux.HandleFunc("/api/v1/dashboard/stats", handler.DashboardStats)
	mux.HandleFunc("/api/v1/topology/global", handler.GlobalTopology)
	mux.HandleFunc("/api/v1/topology/node/", handler.TopologyNodeDetail)
	mux.HandleFunc("/api/v1/topology/sync", handler.SyncTopologyFromK8s)
	mux.HandleFunc("/api/v1/topology/sync-catalog", handler.SyncTopologyCatalog)
	// Topology graph catalogue (typed property graph, aligned with ongrid)
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
	mux.HandleFunc("/api/v1/logs/victorialogs", handler.ProxyVictoriaLogs)
	// Infrastructure (K8s)
	mux.HandleFunc("/api/v1/infrastructure/nodes", handler.Nodes)
	mux.HandleFunc("/api/v1/infrastructure/pods", handler.Pods)
	mux.HandleFunc("/api/v1/infrastructure/deployments", handler.Deployments)
	mux.HandleFunc("/api/v1/infrastructure/namespaces", handler.Namespaces)
	// Settings (LLM + K8s)
	mux.HandleFunc("/api/v1/settings/llm", handler.SettingsLLM)
	mux.HandleFunc("/api/v1/settings/llm/internal", handler.GetInternalLLMSettings)
	mux.HandleFunc("/api/v1/settings/llm/test", handler.TestLLMConnection)
	mux.HandleFunc("/api/v1/settings/llm/models", handler.ModelsLLM)
	mux.HandleFunc("/api/v1/settings/llm/history", handler.ListLLMHistory)
	mux.HandleFunc("/api/v1/settings/llm/providers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.ListLLMProviders(w, r)
		case http.MethodPost:
			handler.CreateLLMProvider(w, r)
		default:
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/api/v1/settings/llm/providers/", func(w http.ResponseWriter, r *http.Request) {
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
	})
	mux.HandleFunc("/api/v1/settings/llm/history/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "rollback") {
			handler.RollbackLLMConfig(w, r)
		} else {
			w.WriteHeader(404)
		}
	})
	mux.HandleFunc("/api/v1/settings/k8s", handler.GetK8sSettings)
	// DeepFlow integration
	mux.HandleFunc("/api/v1/deepflow/status", handler.DeepFlowStatus)
	// AI proxy
	mux.HandleFunc("/api/v1/ai/chat", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/sessions/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/sessions", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/session/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/shell/check", handler.ProxyAI)
	mux.HandleFunc("/api/v1/mcp/tools", handler.ProxyAI)
	mux.HandleFunc("/api/v1/mcp/call", handler.ProxyAI)
	mux.HandleFunc("/api/v1/health", handler.ProxyAI)
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
	// System (HPA + Cache + Redis)
	mux.HandleFunc("/api/v1/system/status", handler.SystemStatus)
	mux.HandleFunc("/api/v1/system/cache", handler.CacheStats)
	mux.HandleFunc("/api/v1/system/cache/invalidate", handler.InvalidateCache)

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
