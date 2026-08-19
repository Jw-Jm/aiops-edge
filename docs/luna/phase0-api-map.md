# Phase 0 API Map

This map records source-registered routes and the current callers. It is not a target API contract.

## External query-api route families

Registered in `ai-apm-query-go/cmd/api/main.go`:

| Family | Route examples | Handler / boundary | Current caller |
|---|---|---|---|
| Auth/user/tenant | `/api/v1/auth/login`, `/api/v1/users`, `/api/v1/me`, `/api/v1/tenants` | JWT/RBAC/user store | frontend |
| Registry/catalog | `/api/v1/clusters`, `/api/v1/catalog/services`, `/api/v1/devices` | MySQL and K8s-backed handlers | frontend/admin |
| Observability | `/api/v1/services`, `/api/v1/traces`, `/api/v1/metrics/query`, `/api/v1/metrics/query_range`, `/api/v1/logs/query`, `/api/v1/logs/aggregate`, `/api/v1/logs/victorialogs` | ClickHouse/VM/VL query handlers | frontend/orchestrator |
| Topology/dashboard | `/api/v1/topology/*`, `/api/v1/dashboard/*`, `/api/v1/data/sync` | ClickHouse/MySQL/K8s aggregation | frontend |
| Infrastructure | `/api/v1/infrastructure/*`, `/api/v1/nodes/metrics` | K8s/KubeVirt handlers | frontend/orchestrator |
| Alerts/SLO/system | `/api/v1/alerts/*`, `/api/v1/slo*`, `/api/v1/system/*` | ClickHouse/MySQL/system handlers | frontend/alert engine |
| Settings | `/api/v1/settings/llm*`, `/api/v1/settings/k8s` | admin-gated LLM and K8s settings | frontend/admin |
| AI proxy | `/api/v1/ai/*`, `/api/v1/ops/*`, `/api/v1/mcp/*`, `/api/v1/ipmi/*`, `/api/v1/node/*`, `/api/v1/shell/*`, `/api/v1/snmp*` | `ProxyAI` / WebSocket proxy with `X-Internal-Token` | frontend → query-api → orchestrator |
| Health/metrics | `/health`, `/api/v1/health`, `/metrics` | process health and Prometheus metrics | Kubernetes/VM |

## Current orchestrator route families

Registered in `ai-orchestrator/main.py`, `flow_api.py`, and `kg_api.py`:

```text
/api/v1/ai/chat
/api/v1/ai/sessions*, /api/v1/ai/session/{sid}
/api/v1/ai/skills*, /api/v1/ai/agents*, /api/v1/ai/marketplace/*
/api/v1/ai/flows*, /api/v1/ai/flows/{key}/run-legacy
/api/v1/ai/workflows*
/api/v1/ai/kg/*
/api/v1/ai/knowledge*, /api/v1/ai/rules*, /api/v1/ai/nl2sql/*
/api/v1/ai/suggestion/execute, /api/v1/ai/final_report
/api/v1/mcp/tools, /api/v1/mcp/call
/api/v1/ops/tasks*, /api/v1/ops/k8s/*, /api/v1/ops/recovery/*
/api/v1/ops/rca, /api/v1/ops/rca/deep, /api/v1/ops/rca/alert
/api/v1/ops/cases*, /api/v1/ops/anomalies*, /api/v1/ops/reports*, /api/v1/ops/audit-logs
/api/v1/ops/changes*, /api/v1/ops/ws
/api/v1/ipmi/*, /api/v1/node/*
/health, /api/v1/health, /metrics
```

## Current ingest and collector APIs

- Ingest: `POST /v1/traces`, `POST /v1/logs`, `POST /v1/deepflow`, `GET /health`, `GET /metrics`.
- Event collector: `GET /health`, `GET /metrics`; Kubernetes Events and IPMI SEL are collected internally, not exposed as user write APIs.

## Frontend route map

Current `observability-frontend/src/App.tsx` exposes:

```text
/overview
/observability/service /observability/trace /observability/log /observability/vms /observability/grafana
/alerts/events /alerts/rules
/ai/chat /ai/tools /ai/workflows /ai/workflows/editor /ai/workflows/:id
/slo /knowledge /capacity /infra/k8s /hardware /changes /kg /report
/admin/approvals /admin/users /admin/settings
/login and wildcard NotFound
```

The target `/api/v1/investigations*`, `/api/v1/resources*`, and `/internal/v1/*` contracts do not exist at this baseline.
