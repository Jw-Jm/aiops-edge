package bootstrap

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/api"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
	"github.com/observability-platform/ai-apm-query-go/internal/store/migrations"
)

func newHTTPServer(handler *api.Handler, port int) *http.Server {
	mux := buildMux(handler)
	corsHandler := api.CORSMiddleware(mux)
	authHandler := api.AuthMiddleware(corsHandler)
	protected := internalMTLS(authHandler)
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           protected,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func buildMux(handler *api.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	health := api.NewHealthHandler(store.GetDB, migrations.RequireCurrent)

	mux.HandleFunc("/api/v1/auth/login", handler.Login)
	mux.HandleFunc("/api/v1/auth/change-password", handler.ChangePassword)
	mux.HandleFunc("/api/v1/login", handler.Login)

	mux.HandleFunc("/api/v1/users", handler.RequireRole("admin", handler.UserRouter))
	mux.HandleFunc("/api/v1/users/", handler.RequireRole("admin", handler.UserRouter))
	mux.HandleFunc("/api/v1/me", handler.Me)
	mux.HandleFunc("/api/v1/me/scope", handler.MeScope)

	mux.HandleFunc("/api/v1/admin/data-cleanups/preview", handler.RequireRole("admin", handler.DataCleanupPreview))
	mux.HandleFunc("/api/v1/admin/data-cleanups/execute", handler.RequireRole("admin", handler.DataCleanupExecute))
	mux.HandleFunc("/api/v1/admin/data-cleanups/", handler.RequireRole("admin", handler.DataCleanupStatus))

	mux.HandleFunc("/api/v1/catalog/services", handler.RequireRoleForWrite("admin", handler.CatalogRouter))
	mux.HandleFunc("/api/v1/catalog/services/", handler.RequireRoleForWrite("admin", handler.CatalogRouter))

	mux.HandleFunc("/api/v1/devices", handler.RequireRoleForWrite("admin", handler.DeviceRouter))
	mux.HandleFunc("/api/v1/devices/", handler.RequireRoleForWrite("admin", handler.DeviceRouter))

	mux.HandleFunc("/api/v1/clusters", handler.ClusterList)
	mux.HandleFunc("/api/v1/clusters/", handler.RequireRoleForWrite("admin", handler.ClusterRouter))

	mux.HandleFunc("/livez", health.Livez)
	mux.HandleFunc("/readyz", health.Readyz)
	mux.HandleFunc("/health", health.Livez)
	mux.HandleFunc("/api/v1/resources/resolve", handler.ResolveResource)

	mux.HandleFunc("/api/v1/services/overview", handler.ServicePanoramaOverview)
	mux.HandleFunc("/api/v1/services/map", handler.ServicePanoramaMap)
	mux.HandleFunc("/api/v1/services/dependency-matrix", handler.ServiceDependencyMatrix)
	mux.HandleFunc("/api/v1/services/", handler.ServiceDependenciesOrDetail)
	mux.HandleFunc("/api/v1/services", handler.ListServices)
	mux.HandleFunc("/api/v1/traces", handler.ListTraces)
	mux.HandleFunc("/api/v1/traces/", handler.TraceRouter)
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
	mux.HandleFunc("/api/v1/infrastructure/nodes", handler.Nodes)
	mux.HandleFunc("/api/v1/infrastructure/pods", handler.Pods)
	mux.HandleFunc("/api/v1/infrastructure/pods/", handler.PodDetail)
	mux.HandleFunc("/api/v1/infrastructure/deployments", handler.Deployments)
	mux.HandleFunc("/api/v1/infrastructure/namespaces", handler.Namespaces)
	mux.HandleFunc("/api/v1/nodes/metrics", handler.NodesMetrics)
	mux.HandleFunc("/api/v1/infrastructure/hpa", handler.HPA)
	mux.HandleFunc("/api/v1/infrastructure/vms", handler.VMs)
	mux.HandleFunc("/api/v1/infrastructure/vms/", handler.VMDetail)
	mux.HandleFunc("/api/v1/settings/llm", handler.RequireRoleForWrite("admin", handler.SettingsLLM))
	mux.HandleFunc("/api/v1/settings/llm/config", handler.RequireRole("admin", handler.GetLLMAdminConfig))
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
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/v1/settings/llm/providers/", handler.RequireRole("admin", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/enable") {
			handler.EnableLLMProvider(w, r)
			return
		}
		switch r.Method {
		case http.MethodPut:
			handler.UpdateLLMProvider(w, r)
		case http.MethodDelete:
			handler.DeleteLLMProvider(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/v1/settings/llm/history/", handler.RequireRole("admin", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "rollback") {
			handler.RollbackLLMConfig(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	mux.HandleFunc("/api/v1/settings/k8s", handler.GetK8sSettings)
	mux.HandleFunc("/api/v1/deepflow/status", handler.DeepFlowStatus)
	api.RegisterGrafanaRoutes(mux, api.NewGrafanaHandler(api.GrafanaConfigFromEnv()))
	mux.HandleFunc("/api/v1/ai/chat", handler.ProxyChat)
	// Browser chat history is a Query/MySQL-owned, scope-bound projection.  The
	// old Orchestrator SQLite session routes remain only for explicit migration
	// tooling and are never reached by the browser API.
	mux.HandleFunc("/api/v1/ai/session/", handler.ChatSessionRouter)
	mux.HandleFunc("/api/v1/ai/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handler.ListChatSessions(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			handler.ClearChatSessions(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	// Final report is a Query/MySQL-owned transcript export.  It must not proxy
	// the legacy orchestrator SQLite endpoint (retired in production).
	mux.HandleFunc("/api/v1/ai/final_report", handler.GenerateChatReport)
	mux.HandleFunc("/api/v1/ai/runs/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/events", handler.StreamRunEvents)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/graph-context", handler.RunGraphContext)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/cancel", handler.PublicCancelRun)
	mux.HandleFunc("/api/v1/ai/runs/{runID}", handler.GetRunPublic)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/tools", handler.GetRunToolsPublic)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/evidences", handler.GetRunEvidencesPublic)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/evidences/{evidenceID}", handler.GetRunEvidencePublic)
	mux.HandleFunc("/api/v1/ai/actions/", handler.RequireAnyRole([]string{"admin", "approver"}, handler.ActionPublicHandler))
	mux.HandleFunc("/api/v1/ai/actions", handler.RequireAnyRole([]string{"admin", "approver"}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handler.ActionProposalPublicHandler(w, r)
			return
		}
		handler.ActionPublicHandler(w, r)
	}))
	mux.HandleFunc("/api/v1/ai/runs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handler.CreateRunPublic(w, r)
		case http.MethodGet:
			handler.ListRunsPublic(w, r)
		default:
			http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/v1/ai/shell/check", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/suggestion/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/suggestion", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/nl2sql/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/nl2sql", handler.ProxyAI)
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
	mux.HandleFunc("/api/v1/ai/kg/ops/", handler.GraphOpsRouter)
	mux.HandleFunc("/api/v1/ai/kg/", handler.GraphPublicRouter)
	mux.HandleFunc("/api/v1/ai/kg", handler.GraphPublicRouter)
	mux.HandleFunc("/api/v1/shell/ws", handler.ProxyShellWS)
	mux.HandleFunc("/api/v1/mcp/tools", handler.ProxyAI)
	mux.HandleFunc("/api/v1/mcp/call", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/workflows/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/workflows", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ipmi/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ipmi", handler.ProxyAI)
	mux.HandleFunc("/api/v1/node/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/node", handler.ProxyAI)
	mux.HandleFunc("/api/v1/snmp/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/snmp", handler.ProxyAI)
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/v1/ops/", handler.ProxyAI)
	mux.HandleFunc("/metrics", handler.PrometheusMetrics)
	mux.HandleFunc("/api/v1/alerts/rules", handler.AlertRules)
	mux.HandleFunc("/api/v1/alerts/rules/", handler.AlertRuleByID)
	mux.HandleFunc("/api/v1/alerts/events", handler.AlertEvents)
	mux.HandleFunc("/api/v1/alerts/events/", handler.AlertEventRouter)
	mux.HandleFunc("/api/v1/alerts/aggregation", handler.AlertAggregation)
	mux.HandleFunc("/api/v1/alerts/silences", handler.AlertSilences)
	mux.HandleFunc("/api/v1/alerts/silences/", handler.AlertSilenceByID)
	mux.HandleFunc("/api/v1/slo", handler.SLORouter)
	mux.HandleFunc("/api/v1/slo/", handler.SLORouterByID)
	mux.HandleFunc("/api/v1/dashboard/panels", handler.DashboardRouter)
	mux.HandleFunc("/api/v1/dashboard/panels/", handler.DashboardRouterByID)
	mux.HandleFunc("/api/v1/tenants", handler.RequireRoleForWrite("admin", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.ListTenants(w, r)
		case http.MethodPost:
			handler.CreateTenant(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/v1/tenants/", handler.RequireRoleForWrite("admin", handler.DeleteTenant))
	mux.HandleFunc("/api/v1/system/status", handler.SystemStatus)
	mux.HandleFunc("/api/v1/system/cache", handler.CacheStats)
	mux.HandleFunc("/api/v1/system/cache/invalidate", handler.InvalidateCache)
	mux.HandleFunc("/api/v1/system/components", handler.SystemComponents)

	mux.HandleFunc("/internal/v1/query/metrics", handler.InternalQueryMetrics)
	mux.HandleFunc("/internal/v1/query/logs", handler.InternalQueryLogs)
	mux.HandleFunc("/internal/v1/query/traces", handler.InternalQueryTraces)
	mux.HandleFunc("/internal/v1/query/alerts", handler.InternalQueryAlerts)
	mux.HandleFunc("/internal/v1/query/topology", handler.InternalQueryTopology)
	mux.HandleFunc("/internal/v1/query/topology/middleware", handler.InternalQueryTopologyMiddleware)
	mux.HandleFunc("/internal/v1/query/kubernetes", handler.InternalQueryKubernetes)
	mux.HandleFunc("/internal/v1/query/changes", handler.InternalQueryChanges)
	mux.HandleFunc("/internal/v1/query/knowledge", handler.InternalQueryKnowledge)
	mux.HandleFunc("/internal/v1/query/graph", handler.InternalQueryGraph)
	mux.HandleFunc("/internal/v1/query/kubevirt", handler.InternalQueryGraphDomain)
	mux.HandleFunc("/internal/v1/query/hardware/inventory", handler.InternalQueryGraphDomain)
	mux.HandleFunc("/internal/v1/query/hardware/health", handler.InternalQueryGraphDomain)
	mux.HandleFunc("/internal/v1/query/catalog", handler.InternalQueryGraphDomain)
	mux.HandleFunc("/internal/v1/query/network-topology", handler.InternalQueryGraphDomain)
	mux.HandleFunc("/internal/v1/control-plane/runs", handler.InternalControlPlaneRunRouter)
	mux.HandleFunc("/internal/v1/control-plane/runs/", handler.InternalControlPlaneRunRouter)
	mux.HandleFunc("/internal/v1/control-plane/recovery/snapshot", handler.InternalControlPlaneRecovery)
	mux.HandleFunc("/internal/v1/control-plane/settings/recovery-policy", handler.InternalControlPlaneRecoveryPolicy)
	mux.HandleFunc("/internal/v1/control-plane/knowledge-graph", handler.InternalControlPlaneKnowledgeGraph)
	mux.HandleFunc("/internal/v1/security/replay/consume", handler.SecurityReplayConsume)
	mux.HandleFunc("/internal/v1/control-plane/tools", handler.InternalControlPlaneToolRouter)
	mux.HandleFunc("/internal/v1/control-plane/tools/", handler.InternalControlPlaneToolRouter)
	return mux
}
