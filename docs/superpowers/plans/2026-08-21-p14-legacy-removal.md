# P14 Legacy 删除实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按 V9.3 合同 Phase 14（删除旧代码、接口、页面、双主路径）删除已确认无 production caller 的 legacy 候选，跨 3 仓库（ingest-go / orchestrator / frontend），保持全量测试通过与红线 F1-F5。

**Architecture:** 删除前已按 P14.1 call-graph 判定（本计划前置），每个删除项均有 replacement ready + production caller=0 证据。本计划按仓库拆 3 个独立可验证单元，每个单元删除后跑对应测试栈（Go test / Python pytest / tsc+vite build）确认无引用残留。B 类（orchestrator 旧 RCA 接线）不属本次范围——它依赖真实 query-api 数据构造 Evidence，属后续真实环境 Integration Gate，旧 RCA 端点保留为生产主链。

**Tech Stack:** Go（ingest-go）、Python（orchestrator）、React+TS+Vite（frontend）

## Global Constraints

- GIT_ACTION = NONE：全程不 git commit/add/checkout（V9.3 合同要求，实施通过文件修改体现）。
- 红线 F1-F5 保持：禁真实执行、禁 credential 泄漏、禁 Agent 自动 rollback、禁绕过授权。
- Agent≠Execution 隔离：删除不得引入任何 execute/credential/kubeconfig 到 Agent 域。
- 只删确认的 legacy 候选，**禁止删活跃主链**（AI Chat `/ai/chat`、Investigation、Service/Logs/Trace 等）。
- B1（orchestrator 旧 RCA）不删除，保留 `full_rca_analysis`/`node_rca` 作为生产主链。
- C 类（平行 dataclass 模型、event-collector、ModeLegacy reader、ProxyAI 高危 fail-closed）全部保留。
- 每个仓库删除后必须编译 + 全量测试通过 + 引用扫描 0 残留。

---

## Task 1: orchestrator 删除 `/api/v1/ai/flows/{key}/run-legacy` 端点

**Files:**
- Modify: `ai-orchestrator/main.py`（删除 `ai_flow_run` 函数，main.py:968-981）
- Test: 现有 `tests/`（删除后全量 pytest 回归）

**Interfaces:**
- Consumes: 无（删除独立端点）
- Produces: `/api/v1/ai/flows/{key}/run-legacy` 路由不再存在；`_get_brain()`/`execute_sync_full` 保留（被其他路径使用）

背景：P14.1 已确认该端点显式标注 `run-legacy`，走旧图 `brain.execute_sync_full`。前端 `client.ts` 用 `/ai/flows/{key}/run`（非 run-legacy），无前端调用该 legacy 端点。

- [ ] **Step 1: 确认删除点**

读取 `ai-orchestrator/main.py` L955-981，确认 `@app.post("/api/v1/ai/flows/{key}/run-legacy")` + `async def ai_flow_run` 完整函数体。

- [ ] **Step 2: 删除 `ai_flow_run` 函数**

删除 `main.py` 中 `ai_flow_run` 函数（L968-981），保留上方 `ai_flows` / `ai_flow_detail`（只读 DAG 描述，非 legacy）。

```python
# 删除以下整个函数：
# @app.post("/api/v1/ai/flows/{key}/run-legacy")
# async def ai_flow_run(key: str, request: Request, body: dict = None):
#     ...
```

注意：`_get_brain()`、`execute_sync_full` 保留（被 main.py L600/654/753、orchestrator.py 等使用）。

- [ ] **Step 3: 扫描残留引用**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
grep -rn "run-legacy" --include="*.py" .
```
Expected: 0 命中（前端 `runFlow` 用 `/run` 非 `/run-legacy`）。

- [ ] **Step 4: 回归测试**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
python3 -m pytest tests/ -q 2>&1 | tail -20
```
Expected: 全量通过（或仅既有已知 collection error：本机 Python3.9.6 flow_engine 需 3.10，见 Global 备注）。

---

## Task 2: ingest-go 删除 legacy ClickHouse writer 双主路径

**Files:**
- Delete: `ai-apm-ingest-go/internal/clickhouse/`（writer.go / log_writer.go / metrics_writer.go / wal.go 及测试）
- Modify: `ai-apm-ingest-go/cmd/ingest/main.go`（删除 `newLegacyWriters`/`legacyWriterEnabledFromEnv`/`validateLegacyGate` + legacy 接线）
- Modify: `ai-apm-ingest-go/internal/pipeline/ingest.go`（删除 legacy writer nil-guard 分支）
- Modify: `ai-apm-ingest-go/internal/telemetry/writer.go`（删除 `"legacy"` 历史别名）
- Test: 现有 `tests/`（删除后 go build + go test）

**Interfaces:**
- Consumes: 删除 `clickhouse` 包引用；pipeline 不再持有 `p.writer`/`p.metricsWriter` legacy 写
- Produces: 单一 new 写路径（VictoriaMetrics/VictoriaLogs）；`telemetry.ParseMode` 仅接受 `"disabled"/"new"`

背景：P14.1 + 生产实测确认 `LEGACY_WRITER_ENABLED=false` + `TELEMETRY_WRITER_MODE=new`，legacy CH 写运行期已停用，无 production caller。删除代码层面遗留。

- [ ] **Step 1: 删除 `internal/clickhouse/` 目录**

```bash
rm -rf /Users/mssc/Documents/Code/agent/aiops/ai-apm-ingest-go/internal/clickhouse/
```
确认目录内无被其他包引用的导出符号（除 pipeline/main 外）。

- [ ] **Step 2: 清理 `cmd/ingest/main.go`**

删除以下函数与接线：
- 删除 `import "github.com/observability-platform/ai-apm-ingest-go/internal/clickhouse"`（L19）
- 删除 L49-76 的 `legacyEnabled` 读取 + `newLegacyWriters` 调用 + log（L52-76）
- 删除 `legacyWriterEnabledFromEnv`（L487-500）
- 删除 `validateLegacyGate`（L502-511）
- 删除 `newLegacyWriters`（L513-529）
- L93-97 的 `validateLegacyGate(legacyEnabled, telRT.Enabled())` 调用替换为：new 后端必须启用（否则无写路径拒绝启动）。

```go
// 替换 L93-97 为：
if !telRT.Enabled() {
    log.Fatalf("no write path active: telemetry new backend disabled; refusing to start with no data sink")
}
```

- [ ] **Step 3: 清理 `internal/pipeline/ingest.go`**

删除 `p.writer`/`p.metricsWriter` 的 legacy nil-guard 分支（L150-152、L217-219 等），保留 new 链写入。若 pipeline 结构体有 legacy writer 字段，一并删除。

- [ ] **Step 4: 清理 `internal/telemetry/writer.go` `"legacy"` 别名**

```go
// 修改 ParseMode：
case ModeDisabled:  // 删除 case ModeDisabled, "legacy":
    return ModeDisabled, nil
```

- [ ] **Step 5: 清理 legacy gate 测试**

删除 `cmd/ingest/legacy_gate_test.go`、`internal/pipeline/legacy_gate_test.go`、`internal/telemetry/oldwriter_inactive_test.go`（均围绕 legacy 双写 gate）。

- [ ] **Step 6: 编译 + 测试**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-ingest-go
go build ./...
go vet ./...
go test ./... 2>&1 | tail -20
```
Expected: build/vet/test 全绿，无 `clickhouse` 引用残留。

---

## Task 3: frontend 删除死路由页面（workflows/tools/knowledge/kg/slo）

**Files:**
- Delete: `observability-frontend/src/pages/ai/Workflows/`（index/Editor/Detail）
- Delete: `observability-frontend/src/pages/ai/AiTools.tsx`
- Delete: `observability-frontend/src/pages/ai/Knowledge.tsx`
- Delete: `observability-frontend/src/pages/ai/KnowledgeGraph.tsx`
- Delete: `observability-frontend/src/pages/slo/SLO.tsx`
- Delete: `observability-frontend/src/api/workflows.ts`
- Delete: `observability-frontend/src/api/marketplace.ts`
- Modify: `observability-frontend/src/App.tsx`（删除 lazy import + Route）
- Test: `npx tsc --noEmit` + `npx vite build`

**Interfaces:**
- Consumes: 删除 5 个页面 + 2 个专有 API 模块
- Produces: App.tsx 路由仅保留 6 大导航 + Investigation + 活跃 AI Chat；`api/client.ts` 的共享函数保留（listKnowledge/listSLOs 等暂留，避免破坏共享模块，留待 P14 完整清理）

背景：P14.1 确认这些页面均为无导航顶层入口 + 无外部路由引用（`navigate('/slo')` 等 0 命中），`api/workflows.ts` 仅被 Workflows 页引用、`api/marketplace.ts` 仅被 AiTools 引用。活跃 AI Chat（`/ai/chat`）保留。

- [ ] **Step 1: 删除页面目录**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend
rm -rf src/pages/ai/Workflows/
rm -f src/pages/ai/AiTools.tsx src/pages/ai/Knowledge.tsx src/pages/ai/KnowledgeGraph.tsx src/pages/slo/SLO.tsx
rm -f src/api/workflows.ts src/api/marketplace.ts
```

- [ ] **Step 2: 修改 App.tsx 删除引用与路由**

删除 lazy import（L22 AiTools、L27 SLO、L28 Knowledge、L31-33 Workflows*、L39 KnowledgeGraph）与对应 Route（L318-323、L328）。保留 L21 AiChat、L41-43 Investigation。

```tsx
// 删除：
// const AiTools = lazy(() => import('./pages/ai/AiTools'))
// const SLO = lazy(() => import('./pages/slo/SLO'))
// const Knowledge = lazy(() => import('./pages/ai/Knowledge'))
// const Workflows = lazy(() => import('./pages/ai/Workflows'))
// const WorkflowsEditor = lazy(() => import('./pages/ai/Workflows/Editor'))
// const WorkflowsDetail = lazy(() => import('./pages/ai/Workflows/Detail'))
// const KnowledgeGraph = lazy(() => import('./pages/ai/KnowledgeGraph'))
// 以及 Routes 中对应 <Route path="/ai/tools" ...> / <Route path="/slo" ...> / <Route path="/knowledge" ...> / <Route path="/kg" ...> / <Route path="/ai/workflows..." ...>
```

- [ ] **Step 3: 扫描残留 import**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend
grep -rn "Workflows\|AiTools\|KnowledgeGraph\|SLO\|api/workflows\|api/marketplace" src/ --include="*.tsx" --include="*.ts" | grep -v "src/api/client.ts\|SLO\b" 
```
Expected: 无对已删页面/模块的 import（注意 SLO 字符串在 client.ts 的 API 名保留是允许的）。

- [ ] **Step 4: 编译验证**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend
npx tsc --noEmit 2>&1 | tail -20
npx vite build 2>&1 | tail -20
```
Expected: 无类型错误、build 成功。

---

## Task 4: 跨仓库红线与回归验证

**Files:**
- 验证：3 仓库全量测试 + 红线 grep

- [ ] **Step 1: 红线隔离 grep**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
grep -rn "execute\|credential\|kubeconfig\|adapter" ai-apm-ingest-go/internal/ ai-orchestrator/agent_runtime.py ai-orchestrator/agents.py 2>/dev/null | grep -vi "test\|_test\|executor\|execution_contract\|adapter interface" | head -20
```
Expected: Agent 域无 execute/credential/kubeconfig；删除后红线 F1-F5 保持。

- [ ] **Step 2: 三仓库全量回归**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-ingest-go && go test ./... 2>&1 | tail -5
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go && go test ./... 2>&1 | tail -5
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python3 -m pytest tests/ -q 2>&1 | tail -5
cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend && npx tsc --noEmit && npx vite build
```
Expected: 全部通过（orchestrator 已知 collection error 除外）。

- [ ] **Step 3: 确认 GIT_ACTION=NONE**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git status --short 2>&1 | head -20
```
Expected: 显示删除/修改为未提交状态，**不执行任何 git add/commit**。

---

## Self-Review

**Spec coverage（V9.3 §八十 Phase 14）:**
- P14.1 Call Graph Before Delete：本计划每个删除项前置判定（本计划 Task 1/2/3 背景节），删除前证明 replacement ready + caller=0 ✓
- P14.2 Legacy Token Scan：本计划删除项覆盖 run-legacy 端点、legacy CH writer、dead frontend routes ✓
- P14.3 Backend Main-path Removal：Task 2（ingest legacy CH writer）✓
- P14.4 Session/Checkpoint Cleanup：**未覆盖** —— 旧 business session/checkpoint 路径删除需另评估（本机 langgraph 冲突，checkpointer 为既有功能，本次不删）
- P14.5 Writer/Reader Transition Cleanup：生产已 `LEGACY_WRITER_ENABLED=false`+`QUERY_READER_MODE=new`（运行期已停用），本计划删 ingest 代码遗留；query-go ModeLegacy reader 保留（C3）
- P14.6 Frontend Cleanup：Task 3 删 dead pages/routes ✓

**已知范围裁剪（诚实标注）：**
- P14.4 旧 session/checkpoint 删除未纳入本次（依赖 langgraph 栈，非纯删除）。
- `api/client.ts` 共享模块内的 listKnowledge/listSLOs 等函数暂留（避免破坏共享模块，待 P14 完整清理或独立小步）。
- B1（orchestrator 旧 RCA 接线）不做：需真实 query-api 数据构造 Evidence，属真实环境 Integration Gate；旧 RCA 端点保留为生产主链。

**Placeholder scan:** 无 TBD/TODO 占位；每步含实际命令与删除目标。

**Type consistency:** 删除项均通过实际文件路径 + 行号定位，与 P14.1 盘点一致。
