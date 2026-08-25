package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/api"
	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
	"github.com/observability-platform/ai-apm-query-go/internal/store/migrations"
)

// randomPassword 用 crypto/rand 生成 n 字节的十六进制随机密码（n*2 个字符）。
func randomPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "admin-" + hex.EncodeToString([]byte(fmt.Sprintf("%d", n)))
	}
	return hex.EncodeToString(b)
}

// trustedContextVerifyConfigFromEnv loads the independently managed service
// credential and one or more Ed25519 verification keys. Multiple public keys
// support key rotation; incomplete configuration leaves internal requests
// fail-closed rather than accepting a service token by itself.
func trustedContextVerifyConfigFromEnv() (trustedauth.VerifyConfig, error) {
	serviceToken := strings.TrimSpace(os.Getenv("INTERNAL_TOKEN"))
	issuer := strings.TrimSpace(os.Getenv("TRUSTED_CONTEXT_ISSUER"))
	rawKeys := strings.TrimSpace(os.Getenv("TRUSTED_CONTEXT_PUBLIC_KEYS"))
	if serviceToken == "" || issuer == "" || rawKeys == "" {
		return trustedauth.VerifyConfig{}, fmt.Errorf("internal signed-context configuration is incomplete")
	}
	publicKeys := make(map[string]ed25519.PublicKey)
	for _, rawKey := range strings.Split(rawKeys, ",") {
		encoded := strings.TrimSpace(rawKey)
		publicKey, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return trustedauth.VerifyConfig{}, fmt.Errorf("invalid trusted-context public key")
		}
		key := ed25519.PublicKey(publicKey)
		publicKeys[trustedauth.KeyID(key)] = key
	}
	// A2-01：共享 TrustedRequest replay guard——用 MySQL ai_context_replay_guard 替代单进程
	// 内存 cache，使多 query-api Pod / 重启后 nonce 重放保护跨进程一致。issuer/audience 固定
	// 为验证器配置，与 nonce 构成 PK。MySQL 不可用时 fail-closed（internal 请求被拒）。
	rc := api.NewMySQLReplayCache(issuer, "ai-apm-query-go")
	return trustedauth.VerifyConfig{
		Audience: "ai-apm-query-go", Issuer: issuer, PublicKeys: publicKeys,
		ServiceToken: serviceToken, ReplayCache: rc,
	}, nil
}

// runInvocationIssuerFromEnv loads the query-api → orchestrator signing private key
// and directional service credential. Incomplete config disables the issuer
// (ProxyAI keeps fail-closed) rather than producing unsigned privileged calls.
func runInvocationIssuerFromEnv() (*trustedauth.RunInvocationIssuer, error) {
	encodedKey := strings.TrimSpace(os.Getenv("QUERY_TO_ORCHESTRATOR_SIGNING_KEY"))
	serviceToken := strings.TrimSpace(os.Getenv("QUERY_TO_ORCHESTRATOR_TOKEN"))
	if encodedKey == "" || serviceToken == "" {
		return nil, fmt.Errorf("query-to-orchestrator signing key or service token is empty")
	}
	privateKey, err := trustedauth.DecodePrivateKey(encodedKey)
	if err != nil {
		return nil, err
	}
	return trustedauth.NewRunInvocationIssuer(privateKey, serviceToken)
}

// requireDatabase makes the Query API fail closed during startup when its
// authoritative persistence dependency is unavailable. The getter is injected
// for a deterministic unit test; production passes store.GetDB.
func requireDatabase(getter func() *sql.DB) (*sql.DB, error) {
	if getter == nil {
		return nil, fmt.Errorf("mysql getter is not configured")
	}
	db := getter()
	if db == nil {
		return nil, fmt.Errorf("mysql unavailable")
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("mysql ping failed: %w", err)
	}
	return db, nil
}

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	chHost := flag.String("ch-host", "clickhouse.observability.svc.cluster.local", "ClickHouse host")
	chPort := flag.Int("ch-port", 8123, "ClickHouse HTTP port")
	// C-02（报告 §17 / 27.22）：运行时三角色拆分——api（HTTP 服务）/ run-dispatch（outbox
	// 派发循环）/ alert-eval（告警评估循环）。复用同一镜像，--role 决定启动哪些组件。
	// 默认 api：HTTP + dispatch + alert 同进程（兼容既有单进程部署）。
	role := flag.String("role", "api", "runtime role: api | run-dispatch | alert-eval")
	flag.Parse()

	db, err := requireDatabase(store.GetDB)
	if err != nil {
		log.Fatalf("query-api startup blocked: %v", err)
	}

	if h := os.Getenv("CLICKHOUSE_HOST"); h != "" {
		*chHost = h
	}
	if p := os.Getenv("CLICKHOUSE_PORT"); p != "" {
		fmt.Sscanf(p, "%d", chPort)
	}

	// V9.2 Phase 4 (P4.4 cutover)：runtime 不再执行 DDL。
	// 1) 只读 readiness check：校验 schema 版本 + checksum 已就绪（缺失/漂移则 fail-closed）。
	// 2) DML-only bootstrap seed（初始默认面板等幂等数据）。
	// 所有 DDL 与一次性 backfill 由 schema-migrator（aiops_migrator）在初始化 Job 中执行。
	if err := migrations.RequireCurrent(db); err != nil {
		log.Fatalf("schema not ready (read-only checksum check): %v", err)
	}
	if err := store.EnsureBootstrapData(db); err != nil {
		log.Fatalf("bootstrap data: %v", err)
	}

	handler := api.NewHandler(*chHost, *chPort)
	if config, err := trustedContextVerifyConfigFromEnv(); err != nil {
		log.Printf("internal signed-context authorization disabled: %v", err)
	} else {
		api.ConfigureInternalRequestVerifier(config)
	}
	// V9.2 P3.9-B2: query-api → orchestrator RunInvocationContext issuer.
	if issuer, err := runInvocationIssuerFromEnv(); err != nil {
		log.Printf("query-to-orchestrator RunInvocation issuer disabled: %v", err)
	} else {
		api.ConfigureRunInvocationIssuer(issuer)
	}
	// Stage D 接线（报告 §29）：query-api → ai-action-executor 客户端。
	// 用独立 Ed25519 私钥（AI_ACTION_EXECUTOR_SIGNING_KEY）签发 signed ActionExecutionContext，
	// 避免复用 QUERY_TO_ORCHESTRATOR_SIGNING_KEY（那会同时改变 RunInvocation issuer 行为）。
	// executor 持对应公钥（EXECUTOR_VERIFY_KEYS）验签。
	// 未配置 AI_ACTION_EXECUTOR_URL 或签名私钥时执行端点 fail-closed（EXECUTOR_UNAVAILABLE），
	// 不产生未签名/不可达的静默执行。
	if err := api.ConfigureActionExecutionClient(
		os.Getenv("AI_ACTION_EXECUTOR_URL"),
		os.Getenv("AI_ACTION_EXECUTOR_SIGNING_KEY"),
		os.Getenv("EXECUTOR_TOKEN"),
	); err != nil {
		log.Printf("ai-action-executor client disabled (Stage D execute endpoint fail-closed): %v", err)
	}
	if vmURL := os.Getenv("VICTORIA_METRICS_URL"); vmURL != "" {
		handler.SetVMURL(vmURL)
	}
	// C-02：按 role 启动运行组件。
	//   api：HTTP 服务 + dispatch + alert（兼容单进程）
	//   run-dispatch：仅 outbox 派发循环（独立进程，与 api 解耦）
	//   alert-eval：仅告警评估循环（独立进程，K8s Lease 单 Leader）
	runDispatch := *role == "api" || *role == "run-dispatch"
	runAlert := *role == "api" || *role == "alert-eval"

	// P10 (V9.3 Phase 10)：durable outbox dispatcher——可靠派发 RunInvocation 给 orchestrator。
	if runDispatch {
		go handler.RunDispatchLoop(context.Background())
		go handler.RunActionDispatchLoop(context.Background())
	}
	// 27.13：Tool Reconciler——收敛超时/未知的 ToolRun（deadline 扫描 + 统一锁序收敛）。
	if runDispatch {
		go handler.RunToolReconcilerLoop(context.Background(), 30*time.Second)
	}
	// 注入 CH 连接供告警事件持久化（必须先于评估引擎启动）
	if runAlert {
		api.SetAlertCH(handler)
		handler.StartAlertEvaluation()
	}
	if db != nil {
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
	health := api.NewHealthHandler(store.GetDB, migrations.RequireCurrent)

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

	// Clusters: GET list 经 canonical-protected（JWT+canonical tenant+成员，前端集群选择器数据源）；
	// 写/同步（POST create、sync）仍 RequireRoleForWrite fail-closed（当前无迁移权限，一律拒绝）。
	mux.HandleFunc("/api/v1/clusters", handler.ClusterList)
	mux.HandleFunc("/api/v1/clusters/", handler.RequireRoleForWrite("admin", handler.ClusterRouter))

	// Health (no auth required)
	mux.HandleFunc("/livez", health.Livez)
	mux.HandleFunc("/readyz", health.Readyz)
	// Compatibility endpoint: /health remains liveness-only and is not used as
	// the Kubernetes readiness signal.
	mux.HandleFunc("/health", health.Livez)
	// Canonical resource resolution is the supported cluster-scoped boundary.
	mux.HandleFunc("/api/v1/resources/resolve", handler.ResolveResource)

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
	// Grafana 代理（工作流 F1）：dashboard 搜索/浏览端点，配置来自 GRAFANA_ROOT_URL/
	// GRAFANA_API_TOKEN/GRAFANA_TLS_INSECURE env；经 AuthMiddleware 统一 JWT 鉴权。
	api.RegisterGrafanaRoutes(mux, api.NewGrafanaHandler(api.GrafanaConfigFromEnv()))
	// AI proxy
	// P19.6: /api/v1/ai/chat 是对话型 canonical-protected 路由，由 ProxyChat 处理
	// （JWT+tenant+cluster 解析 → ai.chat capability 签名 → orchestrator /internal/v1/chat SSE 流式透传）。
	mux.HandleFunc("/api/v1/ai/chat", handler.ProxyChat)
	// P12：Run API 只读代理（/api/v1/ai/runs/{id} 详情）→ orchestrator ai_runs_api
	mux.HandleFunc("/api/v1/ai/runs/", handler.ProxyAI)
	// P10 (V9.3 Phase 10)：公共 SSE proxy（query-api 直接从持久化事件 replay + live-tail）。
	mux.HandleFunc("/api/v1/ai/runs/{runID}/events", handler.StreamRunEvents)
	// P10 (V9.3 Phase 10)：公共 Control 入口（Browser → query-api cancel）。
	mux.HandleFunc("/api/v1/ai/runs/{runID}/cancel", handler.PublicCancelRun)
	// P10 (V9.3 Phase 10)：公共 Run 详情（直接读 MySQL，消除与 orchestrator 内存 RunStore 的 split-brain）。
	mux.HandleFunc("/api/v1/ai/runs/{runID}", handler.GetRunPublic)
	// C2-4：公共 Run Tool activity（真实 ai_tool_runs，不推断冒充）→ 只读工具执行事实。
	mux.HandleFunc("/api/v1/ai/runs/{runID}/tools", handler.GetRunToolsPublic)
	// Evidence 只读投影由 query-api 持有（不再代理 orchestrator 内存注册表）。
	mux.HandleFunc("/api/v1/ai/runs/{runID}/evidences", handler.GetRunEvidencesPublic)
	mux.HandleFunc("/api/v1/ai/runs/{runID}/evidences/{evidenceID}", handler.GetRunEvidencePublic)
	// Action control plane：GET 详情只需 canonical tenant，写操作按动作类型授权；
	// decision 允许 admin/approver，execute 仍由 executor 策略要求 admin。
	mux.HandleFunc("/api/v1/ai/actions/", handler.RequireAnyRole([]string{"admin", "approver"}, handler.ActionPublicHandler))
	mux.HandleFunc("/api/v1/ai/actions", handler.RequireAnyRole([]string{"admin", "approver"}, handler.ActionPublicHandler))
	// P10 (V9.3 Phase 10)：/api/v1/ai/runs 由 query-api 作为 Run 持久化 owner 处理。
	// POST=创建（JWT 鉴权 + 写 outbox 可靠派发），GET=列表（当前 tenant）。不再代理到 orchestrator。
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
	// 知识图谱查询/构建 API（orchestrator kg_api.py，/api/v1/ai/kg 前缀）
	mux.HandleFunc("/api/v1/ai/kg/", handler.ProxyAI)
	mux.HandleFunc("/api/v1/ai/kg", handler.ProxyAI)
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
	// Prometheus metrics（自监控：query-api 自身指标，免鉴权供 VM 抓取）
	mux.HandleFunc("/metrics", handler.PrometheusMetrics)
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
	// Tenants (List any, Create/Delete admin)
	// 安全(P2-2)：POST /tenants 与 DELETE /tenants/{id} 是写操作，需 admin 角色
	//（GET 列表保持登录可读）。
	mux.HandleFunc("/api/v1/tenants", handler.RequireRoleForWrite("admin", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.ListTenants(w, r)
		case http.MethodPost:
			handler.CreateTenant(w, r)
		default:
			w.WriteHeader(405)
		}
	}))
	mux.HandleFunc("/api/v1/tenants/", handler.RequireRoleForWrite("admin", handler.DeleteTenant))
	// System (HPA + Cache + Redis + Components)
	mux.HandleFunc("/api/v1/system/status", handler.SystemStatus)
	mux.HandleFunc("/api/v1/system/cache", handler.CacheStats)
	mux.HandleFunc("/api/v1/system/cache/invalidate", handler.InvalidateCache)
	mux.HandleFunc("/api/v1/system/components", handler.SystemComponents)

	// P6.2e: canonical internal query endpoints（Phase 6）。
	// 供 orchestrator InternalQueryClient（Phase 7）调用。
	// 统一 strict envelope：TrustedRequestContext ONLY（无 JWT fallback）+ capability + scope match + QueryError semantics。
	mux.HandleFunc("/internal/v1/query/metrics", handler.InternalQueryMetrics)
	mux.HandleFunc("/internal/v1/query/logs", handler.InternalQueryLogs)
	mux.HandleFunc("/internal/v1/query/traces", handler.InternalQueryTraces)
	mux.HandleFunc("/internal/v1/query/alerts", handler.InternalQueryAlerts)
	mux.HandleFunc("/internal/v1/query/topology", handler.InternalQueryTopology)
	mux.HandleFunc("/internal/v1/query/topology/middleware", handler.InternalQueryTopologyMiddleware)
	mux.HandleFunc("/internal/v1/query/kubernetes", handler.InternalQueryKubernetes)
	mux.HandleFunc("/internal/v1/query/changes", handler.InternalQueryChanges)
	mux.HandleFunc("/internal/v1/query/knowledge", handler.InternalQueryKnowledge)
	// P10 (V9.3 Phase 10)：control-plane 持久化端点（orchestrator system principal）。
	mux.HandleFunc("/internal/v1/control-plane/runs", handler.InternalControlPlaneRunRouter)
	mux.HandleFunc("/internal/v1/control-plane/runs/", handler.InternalControlPlaneRunRouter)
	mux.HandleFunc("/internal/v1/control-plane/recovery/snapshot", handler.InternalControlPlaneRecovery)
	mux.HandleFunc("/internal/v1/control-plane/settings/recovery-policy", handler.InternalControlPlaneRecoveryPolicy)
	mux.HandleFunc("/internal/v1/control-plane/knowledge-graph", handler.InternalControlPlaneKnowledgeGraph)
	// A2-01：共享 TrustedRequest replay guard 显式消费（system principal + control_plane.replay.consume）。
	mux.HandleFunc("/internal/v1/security/replay/consume", handler.SecurityReplayConsume)
	// 27.14：Evidence 一次消费（/internal/v1/control-plane/tools/{id}/evidence/consume）。
	mux.HandleFunc("/internal/v1/control-plane/tools", handler.InternalControlPlaneToolRouter)
	mux.HandleFunc("/internal/v1/control-plane/tools/", handler.InternalControlPlaneToolRouter)

	// C-02：仅 api role 启动 HTTP 服务；run-dispatch / alert-eval 只跑后台循环。
	var server *http.Server
	if *role == "api" {
		// Wrap: CORS → Auth
		corsHandler := api.CORSMiddleware(mux)
		authHandler := api.AuthMiddleware(corsHandler)

		// H5 修复（R3）：http.Server 加超时，防止慢客户端/慢请求无限占用连接。
		// WriteTimeout=0（禁用）：P10 公共 SSE（长连接 live-tail）需要无限期写；60s 写超时
		// 会中断长连接 SSE。读写超时仍由 ReadTimeout/IdleTimeout/ReadHeaderTimeout 控制（P1-5）。
		server = &http.Server{
			Addr:              fmt.Sprintf(":%d", *port),
			Handler:           authHandler,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      0,
			IdleTimeout:       120 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
		}
		// Production K8s log shipper → VictoriaLogs
		go handler.StartLogShipper()

		go func() {
			log.Printf("Query API listening on :%d", *port)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		}()
	} else {
		log.Printf("Query API running in role=%s (no HTTP server)", *role)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
	if server != nil {
		// H5 修复（R3）：用 Shutdown 优雅排空（10s 宽限），替代直接 Close 丢弃在途请求。
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}
	log.Println("server stopped")
}
