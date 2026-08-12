# AIChat 内嵌审批 + 多轮分析闭环 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 去掉独立"任务工作台"页面，将处置方案审批直接嵌入 AI Chat 会话；用户确认后执行处置建议，并基于执行结果让 LLM 继续深入分析，循环直至输出最终报告。

**Architecture:** 复用现有 LangGraph `interrupt()`/`Command(resume=...)` 机制。aichat 由 chat 精简模式升级为支持"建议-确认-执行-再分析"的迭代编排：`suggestion` 事件不再落任务工作台，而是作为 SSE 事件让前端渲染确认按钮；新增"确认执行"端点，审批后执行脚本并把结果注入同一 thread，驱动下一轮分析。

**Tech Stack:** FastAPI (Python), LangGraph, React (Antd), SSE

## Global Constraints

- 不得修改 `orchestrator.py` 的 `_deterministic_plan` / `_deterministic_diagnosis`（无 LLM 兜底，生产依赖）
- 保持向后兼容：`/ai/chat` 不传 `session_id` 时仍新建会话；`stream:false` 非流式路径不受影响
- `suggestion` 事件结构（`type/script/service/diagnosis/...`）前端与后端必须一致
- 删除任务工作台页面后，不得残留对 `AiTasks` 的 import/路由/菜单引用（避免编译错误）
- 所有新增端点在无 LLM（deterministic 兜底）下也必须可用
- 遵循国内源 / 离线构建约束；改动涉及 orchestrator 镜像需重新构建部署

---

## File Structure

- `observability-frontend/src/pages/ai/AiChat.tsx` — 改造核心：渲染建议确认卡片、确认/驳回按钮、处理 resume 事件
- `observability-frontend/src/api/client.ts` — 新增 `approveSuggestion` / `rejectSuggestion` API
- `observability-frontend/src/App.tsx` — 删除 AiTasks 路由/菜单/lazy import
- `observability-frontend/src/components/CommandPalette.tsx` — 删除任务工作台快捷项
- `observability-frontend/src/pages/overview/index.tsx` — 删除"前往任务工作台"链接
- `observability-frontend/src/pages/ai/AiTasks.tsx` — 删除整个文件
- `ai-orchestrator/main.py` — `/ai/chat` 改造：suggestion 事件携带可 resume 标识；新增审批/驳回端点；审批后执行并返回
- `ai-orchestrator/orchestrator.py` — 迭代编排逻辑：stream_sync 支持多轮 suggestion

---

### Task 1: 删除任务工作台页面（前端）

**Files:**
- Modify: `observability-frontend/src/App.tsx`
- Delete: `observability-frontend/src/pages/ai/AiTasks.tsx`
- Modify: `observability-frontend/src/components/CommandPalette.tsx`
- Modify: `observability-frontend/src/pages/overview/index.tsx`

**Interfaces:**
- Consumes: 现有 App.tsx 路由/菜单结构
- Produces: 无 AiTasks 引用的干净路由

- [ ] **Step 1: 删除 App.tsx 中 AiTasks 引用**
  - 删除 lazy import 行（`const AiTasks = lazy(...)`）
  - 删除菜单项（`{ key: '/ai/tasks', label: '任务工作台', icon: ... }`）
  - 删除路由 `<Route path="/ai/tasks" element={<AiTasks />} />`

- [ ] **Step 2: 删除 CommandPalette.tsx 任务工作台项**
  - 删除 `{ label: '任务工作台', ... }` 快捷项及跳转

- [ ] **Step 3: 删除 overview/index.tsx "前往任务工作台"链接**
  - 删除跳转 `/ai/tasks` 的按钮/链接

- [ ] **Step 4: 删除 AiTasks.tsx 文件**
  ```bash
  git rm observability-frontend/src/pages/ai/AiTasks.tsx
  ```

- [ ] **Step 5: 构建前端验证无编译错误**
  Run: `cd observability-frontend && npm run build`
  Expected: 无残留 AiTasks 引用错误

- [ ] **Step 6: Commit**
  ```bash
  git add -A
  git commit -m "refactor(frontend): remove task workbench page and references"
  ```

---

### Task 2: 后端 /ai/chat 的 suggestion 事件改为内嵌审批（不落任务工作台）

**Files:**
- Modify: `ai-orchestrator/main.py`（`ai_chat` 流式处理 L247-257 附近）
- Modify: `ai-orchestrator/orchestrator.py`（`stream_sync` 产生 suggestion 事件处）

**Interfaces:**
- Consumes: 现有 `_create_chat_suggestion_task`（将被替换）
- Produces: `suggestion` 事件新增 `event_id`（UUID）字段，用于前端确认/驳回定位；`execute_suggestion(service, script, context)` 保持签名不变

- [ ] **Step 1: 新增 suggestion 事件事件定位字段**
  在 orchestrator.py 产生 `suggestion` 事件处，为每个事件附加 `event_id = uuid4().hex`，并暂存到内存（`self._pending_suggestions[event_id] = event`），供确认/驳回端点用。

- [ ] **Step 2: ai_chat 不再创建任务工作台任务**
  修改 main.py L252-253、L327-329：移除 `_create_chat_suggestion_task` 调用，`suggestion` 事件原样发往前端（含 event_id）。

- [ ] **Step 3: 新增确认/驳回端点**
  新增 `POST /api/v1/ai/suggestion/{event_id}/approve` 与 `/reject`：
  - 从 `_pending_suggestions` 取 event
  - approve: `exec_result = _get_brain().execute_suggestion(service, script, diagnosis)`，把 `{event_id, approved:true, exec_result}` 作为 resume 事件写回该 thread 的队列
  - reject: `{event_id, approved:false}` 写回
  - 返回立即 200（执行在后台流式继续）

- [ ] **Step 4: 测试审批端点无 LLM 兜底可用**
  新增 `tests/test_suggestion_approval.py`，验证 approve/reject 端点返回 200 且能调用 `execute_suggestion`。

- [ ] **Step 5: 运行测试 + Commit**
  Run: `pytest tests/test_suggestion_approval.py -v`
  Expected: PASS
  ```bash
  git commit -m "feat(orchestrator): inline suggestion approval endpoints for aichat"
  ```

---

### Task 3: aichat 前端渲染建议确认卡片 + 确认/驳回

**Files:**
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Modify: `observability-frontend/src/api/client.ts`

**Interfaces:**
- Consumes: `suggestion` SSE 事件（含 `event_id/script/service/diagnosis`），后端 `POST /ai/suggestion/{event_id}/approve|reject`
- Produces: 确认/驳回后向前端推送 resume 事件（`type: resume`, 含 `exec_result`）

- [ ] **Step 1: client.ts 新增 API**
  ```ts
  export const approveSuggestion = (eventId: string) => api.post(`/ai/suggestion/${eventId}/approve`)
  export const rejectSuggestion = (eventId: string) => api.post(`/ai/suggestion/${eventId}/reject`)
  ```

- [ ] **Step 2: AiChat 处理 suggestion 事件**
  在 SSE 循环 L101-115 增加分支：`evName === 'suggestion'` → 渲染一个"处置建议确认卡片"（展示 script/diagnosis + 确认/驳回按钮），追加到 messages（role:'assistant', 含 pendingAction）。

- [ ] **Step 3: 确认/驳回处理 + resume 接收**
  - 点"确认" → `approveSuggestion(eventId)`，卡片显示"已确认，正在执行并继续分析…"
  - 点"驳回" → `rejectSuggestion(eventId)`，卡片显示"已驳回"
  - SSE 收到 `resume` 事件 → 把 exec_result 追加为 assistant 消息（"执行结果: …"），并继续后续 chunk/assistant/done

- [ ] **Step 4: 构建前端验证**
  Run: `cd observability-frontend && npm run build`
  Expected: 无编译错误

- [ ] **Step 5: Commit**
  ```bash
  git commit -m "feat(frontend): inline suggestion approval cards in aichat"
  ```

---

### Task 4: 多轮分析闭环（确认后基于执行结果继续分析直至报告）

**Files:**
- Modify: `ai-orchestrator/orchestrator.py`（`stream_sync` / `_run_dag`）
- Modify: `ai-orchestrator/main.py`（SSE resume 流）

**Interfaces:**
- Consumes: `_pending_suggestions[event_id]`、`execute_suggestion`、LangGraph `Command(resume=...)`
- Produces: 最终 `final_response`（报告）；多轮迭代上限（默认 3 轮）防死循环

- [ ] **Step 1: stream_sync 支持多轮 suggestion**
  在 stream_sync 的图执行中，检测到 `interrupt`（等待审批）时 yield `suggestion` 事件并**暂停**（不继续），等待 resume 队列。

- [ ] **Step 2: resume 驱动下一轮分析**
  approve 端点把 exec_result 写入 thread 的 resume 队列；stream_sync 收到后 `Command(resume={"approved":True, "exec_result":...})` 恢复图，把 exec_result 注入 state，继续下一轮 collect→analyze→plan。

- [ ] **Step 3: 迭代上限保护**
  用 `iterations` channel 计数，≥3 轮或 LLM 判定"无新处置建议"时直接进入 summarize 输出最终报告。

- [ ] **Step 4: 无 LLM 兜底测试**
  新增 `tests/test_loop_iterations.py`：模拟一轮 suggestion + resume，验证 state 累计 exec_result、迭代计数、达到上限后输出报告。

- [ ] **Step 5: 运行全量回归 + Commit**
  Run: `pytest tests/ -v`
  Expected: 全部 PASS
  ```bash
  git commit -m "feat(orchestrator): multi-round suggestion-execute-analyze loop in aichat"
  ```

---

### Task 5: 部署 + 集成验证 + 同步 GitHub

**Files:**
- Modify: `deploy/helm/aiops/values.yaml`（前端 + orchestrator 镜像版本）

- [ ] **Step 1: 构建前端 + orchestrator 镜像**
  - 前端: 离线构建新镜像
  - orchestrator: 用 Linux 容器内 sp.tar.gz 离线构建新 tag

- [ ] **Step 2: 部署**
  `helm upgrade aiops deploy/helm/aiops -n observability --reuse-values --set ...`

- [ ] **Step 3: 实机验证**
  - 任务工作台菜单消失
  - aichat 收到建议 → 渲染确认卡片 → 确认 → 执行 → 继续分析 → 最终报告
  - 历史会话点击可正常打开（回归 Bug1）

- [ ] **Step 4: 提交 + push + PR/merge 同步 GitHub**
  ```bash
  git push origin main
  ```

---

## Self-Review

1. **Spec coverage:** Task1 去页面；Task2 后端内嵌审批；Task3 前端确认卡片；Task4 多轮闭环；Task5 部署同步。覆盖全部需求。
2. **Placeholder scan:** 所有步骤含具体文件/代码/命令，无 TBD。
3. **Type consistency:** `event_id` 在 Task2（后端产生）与 Task3（前端使用）一致；`execute_suggestion(service, script, context)` 签名在 Task2/3/4 一致。
