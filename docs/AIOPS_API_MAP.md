# AIOps V9.2 Phase 1 — API Map

Baseline HEAD: `a8fdb5d`. This map records currently registered routes and known callers. It is a read-only inventory; no write API is called.

## ai-apm-query-go — registered routes (from `cmd/api/main.go`)

Public/domain:
```text
/api/v1/auth/login, /api/v1/login, /api/v1/me
/api/v1/clusters, /api/v1/clusters/
/api/v1/services, /api/v1/services/, /api/v1/catalog/services, /api/v1/catalog/services/
/api/v1/devices, /api/v1/devices/
/api/v1/resources/resolve            ← V9.2 canonical resource resolve
/api/v1/dashboard/panels, /dashboard/resources, /dashboard/stats
/api/v1/health
```
Observability:
```text
/api/v1/metrics/query, /metrics/query_range, /nodes/metrics
/api/v1/logs/query, /logs/aggregate, /logs/victorialogs
/api/v1/traces  (via services), /api/v1/topology
/api/v1/infrastructure/deployments, hpa, namespaces, nodes, pods, pods/, vms, vms/
/api/v1/capacity/forecast, /capacity/instances
```
Alerts/SLO/system:
```text
/api/v1/alerts/aggregation, /alerts/events, /alerts/events/, /alerts/rules, /alerts/rules/, /alerts/silences
/api/v1/slo  (not enumerated separately here)
/api/v1/settings/k8s, /settings/llm, /settings/llm/history, /settings/llm/models, /settings/llm/providers, /settings/llm/internal
/api/v1/deepflow/status, /api/v1/data/sync
```
AI / proxy:
```text
/api/v1/ai/chat, /ai/agents, /ai/final_report, /ai/flows, /ai/flows/, /ai/kg, /ai/knowledge, /ai/knowledge/
/api/v1/ai/nl2sql, /ai/nl2sql/, /ai/rules, /ai/session/, /ai/sessions, /ai/sessions/
/api/v1/ai/shell/check, /ai/skills, /ai/suggestion, /ai/workflows
/api/v1/mcp/call, /api/v1/mcp/tools
/api/v1/node, /api/v1/node/, /api/v1/ipmi, /api/v1/ipmi/
/api/v1/ops/, /api/v1/snmp* (dead proxy noted for removal)
```

**V9.2 mapping:** `POST /api/v1/investigations`, `GET .../{run_id}`, `cancel`, `events`, `actions/*`, and `/internal/v1/*` (run-invocations, run-controls, query/*, control-plane/*, execution/*) are new V9.2 contracts to be added (Phase 5-11). `resources/resolve` + `clusters` already exist and are reused. `/api/v1/ai/chat`, `/ops/rca*`, `/ai/sessions*`, `/suggestion/execute` are old-path routes to be replaced/removed (Phase 14).

## ai-orchestrator — registered routes (from `main.py`)

Old AI Chat / session / suggestion (removal targets Phase 14):
```text
/api/v1/ai/chat, /api/v1/ai/session/{sid}, /api/v1/ai/sessions, /api/v1/ai/suggestion/execute
/api/v1/ai/export/chat/{sid}
```
AI tools / flows / knowledge / kg / nl2sql / skills:
```text
/api/v1/ai/agents, /ai/agents/{name}, /ai/final_report
/api/v1/ai/flows, /ai/flows/{key}, /ai/flows/{key}/run-legacy
/api/v1/ai/knowledge, /ai/knowledge/case, /ai/knowledge/playbooks, /ai/knowledge/rag/*, /ai/knowledge/{kid}
/api/v1/ai/marketplace/*, /ai/nl2sql/*, /ai/rules*, /ai/skills*, /ai/shell/check
```
Ops / RCA / recovery / reports / tasks (old-path RCA targets Phase 14):
```text
/api/v1/ops/rca, /ops/rca/alert, /ops/rca/deep, /ops/rca/alert/export
/api/v1/ops/anomalies*, /ops/artifacts, /ops/audit-logs, /ops/cases*, /ops/changes*, /ops/export
/api/v1/ops/k8s/execute, /ops/k8s/preflight
/api/v1/ops/recovery/plan, /ops/recovery/policy
/api/v1/ops/reports*, /ops/tasks*, /ops/webhook, /ops/ws
```
IPMI / shell / node / mcp:
```text
/api/v1/ipmi/events, /ipmi/ingest, /ipmi/sensors
/api/v1/shell/ws, /api/v1/node/health, /api/v1/node/health/aggregate
/api/v1/mcp/call, /api/v1/mcp/tools
/api/v1/health
```

**V9.2 mapping:** orchestrator old AI/RCA/session/task routes are superseded by the new investigation chain. V9.2 requires query-api to be the browser's only trust boundary (browser never reaches orchestrator directly), and orchestrator only accepts signed internal contexts. New V9.2 internal routes are added by query-api (Phase 5/6) and consumed by orchestrator (Phase 7-11).

## Browser → query-api → orchestrator (current, to be retained/adjusted)

```text
Browser → query-api (JWT) → (ProxyAI / signed context) → orchestrator → query-api (TrustedRequestContext) → storages
```

Current `_request_context_from_request` in orchestrator `main.py` already decodes `X-Trusted-Request-Context` (issuer=query-api, audience=ai-orchestrator, 403 invalid_context otherwise). This is consistent with V9.2's trust direction and is retained.
