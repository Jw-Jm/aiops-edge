# Trace Summary/Index 查询层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Trace 列表从 `trace_spans` 明细全量高基数聚合切换到可持续扩展的预聚合 Summary/Index 层。

**Architecture:** `trace_spans` 保持明细 SoT；ClickHouse `AggregatingMergeTree` 的 `trace_summary_state` 通过物化视图增量接收 Span 聚合状态，`trace_summary_index` 按日期分区和负纳秒时间键提供最新优先的候选 Trace ID，并由一次性分区回填补齐既有数据。query-api 先查 Index，再对候选 ID 查 Summary，详情仍按 Trace ID 读取明细。

**Tech Stack:** Go 1.25、ClickHouse 24.8 HTTP、Helm、Vitest、Bash 契约测试。

**Spec:** `docs/superpowers/specs/2026-08-27-trace-summary-index-design.md`

## Global Constraints

- DeepFlow 源码不修改；DeepFlow ClickHouse 只由 DeepFlow 自身拥有。
- `trace_spans` 是 Trace 明细 SoT；`trace_summary_state` 只承担列表 Summary/Index。
- `trace_summary_index` 只承担时间有序候选定位，不承载 Trace 详情或事实统计。
- 列表查询禁止从 `trace_spans` 做 `GROUP BY trace_id`，不得用抽样或伪造数据补空。
- DeepFlow Agent 熔断继续关闭，除非用户明确要求恢复。
- AI Chat 本轮不调用、不发送真实 LLM 请求。
- 本次架构故障修复必须构建、部署并用真实数据验证；GitHub 按累计五个故障批次同步。

---

### Task 1: 锁定 Summary SQL 与 schema 契约

**Files:**
- Create: `deploy/helm/aiops/files/clickhouse/migrations/0003_trace_summaries.sql`（迁移文件中的对象名为 `trace_summary_state`）
- Modify: `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`
- Modify: `deploy/scripts/test-deployment-contracts.sh`
- Test: `deploy/scripts/test-deployment-contracts.sh`

- [ ] 写失败的 Helm/DDL 契约：要求 Summary 表、聚合状态列、物化视图和 `span_dedup_key` 去重表达式存在。
- [ ] 运行 `bash deploy/scripts/test-deployment-contracts.sh`，确认在 DDL 尚未加入时按预期失败。
- [ ] 增加幂等的 Summary 表和 MV DDL；初始化脚本与版本迁移保持同一字段/表达式。
- [ ] 重新运行契约测试并使用 `helm template` 检查 DDL 被打包到初始化 ConfigMap。

### Task 2: 建立历史回填工具/任务

**Files:**
- Create: `deploy/scripts/backfill-trace-summaries.sh`
- Modify: `deploy/helm/aiops/templates/clickhouse/init-job.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Test: `deploy/scripts/test-deployment-contracts.sh`

- [ ] 写失败的脚本契约：回填必须按 `date` 分区、只读 `trace_spans`、写 `trace_summary_state`，不得全表一次性无界执行。
- [ ] 运行契约测试确认失败原因来自脚本缺失。
- [ ] 实现带 cutoff、分区循环、外部聚合和可重复状态输出的回填任务；回填不删除明细。
- [ ] 对空表、已回填分区、部分失败分别返回明确状态，并将任务设置为独立后台 Job，不阻塞 query-api 启动。
- [ ] 运行脚本静态契约和 shellcheck/Helm lint（可用部分）。

### Task 3: 将 query-api Trace 列表切换到 Summary

**Files:**
- Modify: `ai-apm-query-go/internal/query/traces.go`
- Modify: `ai-apm-query-go/internal/query/traces_test.go`
- Modify: `ai-apm-query-go/internal/api/handler_test.go`

- [ ] 先把现有 repository 测试改成捕获 Summary SQL，断言 `trace_summary_state FINAL`、`finalizeAggregation`、时间/服务/关键字过滤，并断言没有 `FROM observability.trace_spans`。
- [ ] 运行 `go test ./internal/query ./internal/api`，确认测试在切换实现前失败。
- [ ] 实现 Summary 查询、TabSeparated 解析和分页；保留 `FindSpans`/详情路径不变。
- [ ] 移除当前仅为临时缓解而添加的明细聚合 settings，不以提高内存为最终依赖。
- [ ] 运行上述 Go 测试并补充关键字/服务/跨日期分页测试。

### Task 4: 真实部署、回填和端到端验收

**Files:**
- Modify: `deploy/scripts/verify-aiops-workflow-gates.sh`（仅在需要接入已有门禁时）
- Test: `deploy/scripts/backfill-trace-summaries.sh`、真实 API、浏览器 Trace 页面

- [ ] 构建 query-api/DDL 相关镜像并执行 Helm 升级；不触发 AI Chat 或高风险业务操作。
- [ ] 在本机 ClickHouse 创建/迁移 Summary 表和 MV，记录迁移状态；按日期分区回填当前租户历史数据。
- [ ] 真实核对 Summary 行数、最新时间、最新 Span 与 Summary 的关联，以及最新 OTLP 写入持续增量。
- [ ] 连续请求 `/api/v1/traces`，确认 200、50 条真实数据、无 `MEMORY_LIMIT_EXCEEDED`，并检查 query-api/ClickHouse 日志。
- [ ] 浏览器验证 1h/6h/24h、服务筛选、关键字、加载更多、详情瀑布图及错误状态。
- [ ] 重跑完整门禁和 Helm 契约，重新读取 DeepFlow 熔断配置。

### Task 5: 三方版本与批次同步

**Files:**
- Modify: only tracked implementation files from Tasks 1–4

- [ ] 检查工作区未跟踪的用户文档不被加入提交。
- [ ] 本地提交本次已验证修复，确认部署镜像和 Git revision 可追溯。
- [ ] 累计满足五个故障修复后，向用户明确展示待推送提交范围；获得远端具体授权后推送 `origin/main`，再读取远端 HEAD 与本地 HEAD 对齐证据。
