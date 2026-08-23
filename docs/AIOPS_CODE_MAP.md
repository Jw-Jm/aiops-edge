# AIOps V9.2 Phase 1 — Code Map

Baseline HEAD: `a8fdb5d`. This map records the real modules and their mapping to the V9.2 contract. Where V9.2 names a module differently from the real code, the real path is recorded and mapped, without redesign.

## observability-frontend (React 18, TS, Vite, Ant Design, Zustand)

- Entry: `src/main.tsx`; shell/nav/routing: `src/App.tsx`
- Unified API client: `src/api/client.ts`; existing contract types: `src/api/contracts.ts`
- UI cluster state: `src/store/uiStore.ts`; identity: `src/store/authStore.ts`, `src/components/RequireAuth.tsx`
- AI dock: `src/components/AiDock.tsx`
- Runtime boundary: `nginx.conf` proxies API to query-api
- Pages (current): Login, NotFound, Overview; admin/AdminSettings, AdminUsers, Approvals; ai/AiChat, AiTools, Knowledge, KnowledgeGraph, Workflows/{index,Editor,Detail}; alerts/AlertEvents, AlertRules; capacity/Capacity; infra/Changes, Hardware, K8sActions; observability/Grafana, LogMetrics, ServiceObservability, Trace, VirtualMachines; report/Report; slo/SLO

**V9.2 mapping:** `ai/AiChat.tsx` → replaced by Investigation page (Phase 12); `ai/AiTools.tsx`, `KnowledgeGraph.tsx`, `Workflows/*` → downgraded/hidden (Phase 12/14); `LogMetrics.tsx` gains raw-log/pattern dual view (Phase 12); `ServiceObservability/Trace/K8sActions/AlertEvents` gain "交给 AI 调查" (Phase 12).

## ai-apm-query-go (Go)

- Entry: `cmd/api/main.go` (route registration)
- Query/proxy handlers: `internal/api/`; MySQL DAO: `internal/store/`; auth/JWT/RBAC: `internal/api/auth.go`, `internal/auth/`
- Business aggregation: `internal/biz/dashboard.go`; contracts: `internal/contract/` (context.go, context_test.go, fixture_test.go)
- Cluster registry: `internal/api/clusters.go`, `internal/store/clusters.go`; settings/ProxyAI: `internal/api/settings.go`
- Production image: `Dockerfile`; carries `kubectl`

**V9.2 mapping:** existing `internal/contract` + `cmd/api/main.go` routes reused; `resources/resolve` + `clusters` already present. Phase 2 freezes contracts; Phase 3 adds canonical cluster UUID / JWS / nonce / ServiceAuthenticator / Resource Resolver; Phase 5/6 writer/reader; Phase 11 Execution Adapter in-process.

## ai-apm-ingest-go (Go)

- Entry: `cmd/ingest/main.go`; loadgen: `cmd/loadgen/main.go`
- Models: `internal/model/span.go`, `log.go`, `metrics.go`; pipeline: `internal/pipeline/ingest.go`, `deepflow.go`, `deepflow_sync.go`
- ClickHouse writes: `internal/clickhouse/writer.go`, `log_writer.go`, `metrics_writer.go`; WAL: `internal/clickhouse/wal.go`
- Production image: `Dockerfile`; image name `ingest-pipeline`

**V9.2 mapping:** Phase 4 new schema; Phase 5 writer refactor to add tenant_id/cluster_id/resource_id (keep WAL).

## ai-event-collector (Go)

- Entry: `main.go`; config `config.go`; K8s events `k8s_events.go`; IPMI SEL `sel_events.go`; ClickHouse writer `clickhouse.go`
- Production image: `Dockerfile`; retains `ipmitool`

**V9.2 mapping:** Phase 4 unified schema (no self-DDL); Phase 5 single-leader, WAL/outbox, checkpoint key tenant+cluster+source.

## ai-orchestrator (Python/FastAPI)

- Entry + routes: `main.py`; old LangGraph graph: `orchestrator.py`; free-text `AgentState`
- Tools: `tools.py`, `function_calling.py`, `mcp_server.py`; registry: `skill_registry.py`, `skill_loader.py`, `skills/`, `persona_registry.py`
- RCA: `rca.py`, `investigator.py`, `detector.py`; approval/audit: `db_approval.py`, `db_audit.py`, `execution_gate.py`
- K8s actions: `k8s_actions.py`; shell: `shell_policy.py`, `shell_ws.py`
- RAG/knowledge: `rag.py`, `playbook_loader.py`, `knowledge_seed.py`, `data/playbooks/`
- Graph: `kg_graph.py`, `kg_tools.py`, `kg_api.py`; independent Flow Engine: `flow_api.py`, `flow_engine/`
- Contracts: `contracts.py` (V9.2-relevant; 7 tests pass)
- Migrations: `migrations/0001_business_tables.sql` .. `0004_change_events.sql`
- Offline assets: `bin/` (chroma.tar.gz, hf.tar.gz, k8sgpt, kubectl, pybin.tar.gz, sp.tar.gz, ~779MB)
- Production image: `Dockerfile`; image name `ai-orchestrator` (2.6GB uncompressed)

**V9.2 mapping:** existing dual paths (AI Chat / prompt RCA / Tool Router / Workflow investigation / Session/Checkpoint business history) are to be removed (Phase 14); new investigation path built Phase 7-11. Old `AgentState` free-text fields → structured `AIOpsState` (Phase 7).

## ipmi-exporter (Python)

- `collect.py`, `build.sh` — host/IPMI runtime boundary; in scope, no Phase 1 change.

## deploy (Helm + scripts)

- Chart: `deploy/helm/aiops/`; ClickHouse init: `files/clickhouse/init_clickhouse.sql`; MySQL migrations: `files/mysql/migrations/0001_init.sql`; values: `values.yaml`, `values-prod.yaml`, `values-deepflow.yaml`
- Build: `deploy/scripts/build-images.sh`; apply: `deploy/scripts/apply.sh`; version: `deploy/scripts/version.sh`
- Deploy tests: `test-default-deploy.sh`, `multicluster-demo.sh`
