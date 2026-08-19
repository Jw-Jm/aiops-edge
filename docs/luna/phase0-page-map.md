# Phase 0 Page Map

## Current information architecture

Source: `observability-frontend/src/App.tsx`.

| Navigation group | Current entries | Main source |
|---|---|---|
| Overview | 总览 | `pages/Overview/index.tsx` |
| Observability | 服务全景、链路追踪、日志与指标、虚拟机、Grafana 面板 | `pages/observability/*` |
| Alerts | 告警事件、告警规则 | `pages/alerts/*` |
| AI | AI 对话、图谱视图、AI 工具、工作流、SLO、知识库 | `pages/ai/*`, `pages/slo/SLO.tsx` |
| Resources | 容量预测、K8s 运维、硬件健康 | `pages/capacity`, `pages/infra` |
| Operations | 报告中心、变更时间线 | `pages/report`, `pages/infra/Changes.tsx` |
| Administration | 审批中心、用户管理、系统设置 | `pages/admin/*` |

## Current page and API coupling

- `AiChat.tsx` consumes session/chat/progress/suggestion/RCA APIs through `src/api/client.ts`.
- `AiTools.tsx` consumes MCP, skills, agents, marketplace, and knowledge/tool APIs.
- Workflow pages consume `src/api/workflows.ts` and `/ai/workflows*`.
- KnowledgeGraph consumes KG endpoints and is independently reachable at `/kg`.
- `LogMetrics.tsx` currently exposes both ClickHouse and VictoriaLogs source choices and has logs/aggregate tabs, but no required Pattern/Baseline/Current/Growth/First Seen contract.
- `ServiceObservability.tsx`, `Trace.tsx`, `K8sActions.tsx`, `AlertEvents.tsx`, and resource pages do not yet share the target Investigation/ResourceRef/Evidence contract.
- `ClusterSwitcher.tsx` exposes an `all` option and the frontend state is not yet the authoritative scope boundary.

## Target gap recorded for later phases

The target navigation in `aiops-agentic.md` is not the current navigation. AI Chat must become Investigation; Tool/Graph/Workflow/Report/Grafana technical surfaces must move to embedded or admin contexts; professional pages must gain bidirectional Evidence links and frozen tenant/cluster/resource/time context.
