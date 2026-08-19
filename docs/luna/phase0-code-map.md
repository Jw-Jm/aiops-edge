# Phase 0 Code Map

Baseline SHA: `6823ea29b6294e7f00ebb1f088b6e772ad80028a`
Source branch: `main`
Implementation worktree: `codex/aiops-phase0`

## Service map

| Service | Language/runtime | Entrypoint | Primary responsibility | Key dependencies / callers |
|---|---|---|---|---|
| `observability-frontend` | React 18, TypeScript, Vite, Ant Design, Zustand | `src/main.tsx`, `src/App.tsx` | authenticated console, navigation, professional pages, AI Chat, workflows, knowledge, graph | browser; nginx proxies API to query-api |
| `ai-apm-query-go` | Go 1.25 | `cmd/api/main.go` | JWT/RBAC boundary, public REST API, ClickHouse/VM/VL/MySQL/K8s access, AI proxy, alert engine | frontend nginx; orchestrator proxy; storage backends |
| `ai-apm-ingest-go` | Go 1.23 | `cmd/ingest/main.go` | OTLP traces/logs, DeepFlow, topology aggregation, ClickHouse writers, VictoriaMetrics, WAL | OTLP/DeepFlow clients; ClickHouse/VM |
| `ai-event-collector` | Go 1.21 | `main.go` | Kubernetes Events and IPMI SEL collection, ClickHouse writer | Kubernetes API/IPMI; ClickHouse |
| `ai-orchestrator` | Python/FastAPI/LangGraph | `main.py`, `orchestrator.py` | AI chat, skills/tools, RCA, RAG, KG, workflows, approvals, K8s actions, reports | query-api via HTTP/internal token; MySQL; optional Chroma/LLM |
| `ipmi-exporter` | Python exporter | `collect.py` | IPMI/node hardware metrics export | deployment/host IPMI boundary |
| Helm/deploy | Helm/YAML/shell | `deploy/helm/aiops`, `deploy/scripts` | schema initialization, workloads, secrets, network policy, build/deploy | local Kubernetes, Docker |

## Current architectural facts requiring later change

- `ai-apm-query-go/cmd/api/main.go` is a single large route registration point and wraps handlers with CORS and JWT middleware.
- `ai-apm-query-go/internal/store/mysql.go` performs schema creation/ALTERs at service startup; `clusters.kubeconfig` is currently a MySQL text column.
- `ai-apm-query-go/internal/store/clusters.go` reads and writes kubeconfig and uses numeric IDs/name-based lookup.
- Query-api proxies `/api/v1/ai/chat`, sessions, old RCA, workflows, KG, IPMI, node, SNMP, MCP, and ops paths to orchestrator.
- `ai-orchestrator/main.py` contains the old Chat/Session/Task/RCA/Flow/Knowledge/Tool/Action API families in one application module.
- `ai-orchestrator` currently has multiple execution/dispatch surfaces: `tools.py`, `skill_registry.py`, `function_calling.py`, `mcp_server.py`, Flow Engine, and LangGraph orchestration.
- Existing orchestrator modules include direct query-api calls and an internal-token model; the target Ed25519/JWS context model is not implemented.
- Ingest retains independent WAL components in `internal/clickhouse/wal.go`; this is a protected reliability mechanism.
- Event collector has a runtime ClickHouse writer and must be checked for service-owned DDL before Phase 4.

## Existing file families

```text
ai-apm-query-go/cmd/api/main.go
ai-apm-query-go/internal/api/*.go
ai-apm-query-go/internal/store/*.go
ai-apm-ingest-go/cmd/ingest/main.go
ai-apm-ingest-go/internal/pipeline/*.go
ai-apm-ingest-go/internal/clickhouse/*.go
ai-event-collector/{main.go,k8s_events.go,sel_events.go,clickhouse.go}
ai-orchestrator/{main.py,orchestrator.py,tools.py,skill_registry.py,function_calling.py,mcp_server.py}
ai-orchestrator/{rca.py,investigator.py,detector.py,k8s_actions.py,execution_gate.py,shell_policy.py}
ai-orchestrator/{flow_api.py,flow_engine/,kg_api.py,kg_graph.py,rag.py,playbook_loader.py}
observability-frontend/src/{App.tsx,api/,store/,components/,pages/}
deploy/helm/aiops/{templates/,files/,values*.yaml}
```
