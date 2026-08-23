# AIOps V9.2 Phase 1 — Frontend Map

Baseline HEAD: `a8fdb5d`. Read-only inventory of frontend structure, navigation, pages, API client, and state.

## Stack

React 18, TypeScript, Vite, Ant Design, Zustand. Build: `npm run build` (PASS, 4.30s). `node_modules` present.

## Entry & shell

- Entry: `src/main.tsx`
- Shell / nav / routing: `src/App.tsx` (NAV_GROUPS / routes)
- Unified API client: `src/api/client.ts`
- Existing contract types: `src/api/contracts.ts` (V9.2-relevant; typechecked)
- UI cluster state: `src/store/uiStore.ts` (currentClusterId)
- Identity: `src/store/authStore.ts`, `src/components/RequireAuth.tsx`
- AI dock: `src/components/AiDock.tsx`
- Runtime boundary: `nginx.conf` proxies API to query-api

## Pages (current)

```text
Login, NotFound, Overview
admin/AdminSettings, admin/AdminUsers, admin/Approvals
ai/AiChat, ai/AiTools, ai/Knowledge, ai/KnowledgeGraph, ai/Workflows/{index,Editor,Detail}
alerts/AlertEvents, alerts/AlertRules
capacity/Capacity
infra/Changes, infra/Hardware, infra/K8sActions
observability/Grafana, observability/LogMetrics, observability/ServiceObservability,
observability/Trace, observability/VirtualMachines
report/Report, slo/SLO
```

## Current primary navigation exposure (V9.2 to converge)

Still exposed as primary product entries: AI 对话 (AiChat), 图谱视图 (KnowledgeGraph), AI 工具 (AiTools), 工作流 (Workflows), 报告中心 (Report), 变更时间线 (Changes), 容量预测 (Capacity), 独立 Grafana. Per V9.2 these must not remain ordinary primary entries after Phase 12.

## V9.2 target navigation (Phase 12)

```text
总览
智能运维: 智能调查 / 告警与事件 / 审批任务
可观测: 服务 / 链路 / 日志与指标
资源: Kubernetes / 主机与虚机 / 容量与硬件
治理: 知识与 Runbook / 变更与审计 / SLO
系统管理: 用户与权限 / 设置 / 高级
```

## Key frontend transformations planned (mapped to V9.2 phases)

- Phase 12: rewrite `App.tsx` to fixed IA; replace `ai/AiChat.tsx` with Investigation page; add `api/investigations.ts`; add investigation components (Plan/Evidence/Hypothesis/Remediation/Verification panels); hide/downgrade AiTools/Workflows/KnowledgeGraph primary entries.
- Phase 12: LogMetrics gains 原始日志 / 异常模式 dual view; Service/Trace/K8s/Alert pages gain "交给 AI 调查".
- Phase 12: Evidence deep links use canonical resource params; run/evidence cluster never overridden by global selector.
- Phase 12: Run URL share requires re-RBAC (no anonymous/cross-tenant share).
- Phase 12: SSE reconnect resumes from persisted run.
- Phase 13: localStorage role tampering cannot raise privilege; frontend buttons are UX only, server is the boundary.
- Phase 14: remove dead pages/routes/API wrappers/state/styles.

## Build observations

Production build PASS with chunk-size warnings (informational). Playwright E2E dependency currently UNAVAILABLE (Phase 19 prerequisite).
