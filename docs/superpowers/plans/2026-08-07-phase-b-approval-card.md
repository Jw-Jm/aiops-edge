# Phase B 剩余 · Approval 内联审批卡（最简路径）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 SSE 聊天流中发送 `approval_pending` 事件（携带 task_id/plan/script/risk），前端 AIChat/ChatThread 渲染内联审批卡（批准/拒绝按钮 → 现有 `/ops/tasks/{id}/approve|reject`）。不动 interrupt/resume 机制，改造面最小。

**Architecture:** 后端 `orchestrator.py stream_sync` chat 分支：从 `suggestion` 事件升级为 `approval_pending`（复用 `_create_chat_suggestion_task` 建 waiting 任务 + 回传 task_id）；前端 AIChat/ChatThread 补 `approval_pending` 分支渲染审批卡（展示 plan/script/risk + 批准/拒绝）。

**Tech Stack:** Python FastAPI / React18 / AntD5。

## Global Constraints

- 后端：`orchestrator.py`（`stream_sync` 772-832，chat 分支 `suggestion` 事件 827）、`main.py`（`_create_chat_suggestion_task` 434、`/ops/tasks/{tid}/approve` 612、`/reject` 645）。
- **现状**：chat 模式 `approved: True` 硬编码（781）；后端不发 `approval_pending`；前端 `approval_pending` 是空占位分支（AIChat 171/ChatThread）。
- **设计（最简路径）**：`stream_sync` chat 分支提取脚本后发 `approval_pending`（含 `task_id`），`main.py ai_chat` 捕获后建 waiting 任务并回传 task_id；前端渲染审批卡调现有接口。**不新增中断/恢复机制**；审批执行结果走现有 `approve_task` 的 `execute_suggestion`。
- 合规：独立实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`f543fdb`，每任务提交。

---

## Task 1: stream_sync chat 分支发 approval_pending 事件（含 task_id）

**Files:**
- Modify: `aiops/ai-orchestrator/orchestrator.py`（`stream_sync` chat 分支）

**Interfaces:**
- Consumes: 现有 `_extract_script`（823-825）、chat 分支的 `suggestion` yield（827）。
- Produces: 提取到脚本时 yield `{"type":"approval_pending","task_id":tid,"plan","script","risk_score","risk_reason"}`（替代/补充 `suggestion`），`tid` 由调用方传入或生成。

- [ ] **Step 1: 修改 chat 分支 suggestion → approval_pending**

```python
# orchestrator.py stream_sync chat 分支（现 line ~825-832）
# 原：yield {"type": "suggestion", "text": ..., "script": script}
# 改为（保留 suggestion 兼容 + 新增 approval_pending）：
if script:
    # 生成或复用 task id（由调用方通过 ctx 传入，或生成）
    pending_tid = context.get("suggestion_task_id", f"sug_{int(time.time()*1000)}")
    yield {"type": "suggestion", "text": f"已生成操作建议（任务 {pending_tid}）", "script": script}
    yield {"type": "approval_pending",
           "task_id": pending_tid,
           "plan": "执行提取的运维命令",
           "script": script,
           "risk_score": context.get("risk_score", 0.3),
           "risk_reason": "需要人工确认后执行",
           "requires_approval": True}
```
> 注：`context` 为 `stream_sync` 的入参 dict（含 thread_id 等）。`suggestion_task_id` 由调用方（main.py `ai_chat`）通过 `_create_chat_suggestion_task` 生成后塞入 context。

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile orchestrator.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/orchestrator.py
git commit -m "feat(orchestrator): emit approval_pending event in chat stream"
```

---

## Task 2: main.py 捕获 approval_pending + 回传 task_id

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`（`ai_chat` 163-200、`_create_chat_suggestion_task` 434）

**Interfaces:**
- Consumes: Task 1 的 `approval_pending` 事件。
- Produces: `ai_chat` 捕获 `approval_pending` → `_create_chat_suggestion_task` 建 waiting 任务 → 把 `task_id` 注入事件供前端绑定；approve/reject 复用现有接口。

- [ ] **Step 1: ai_chat 捕获 approval_pending 并注入 task_id**

在 `ai_chat` 的 SSE 事件转发（`_format_sse` 处，main.py ~174）：
```python
# 若事件是 approval_pending，确保 task_id 已生成
if event.get("type") == "approval_pending":
    # 复用/生成 suggestion 任务
    tid = _ensure_suggestion_task(event, ...)  # 见 Step 2
    event["task_id"] = tid
```
新增辅助 `_ensure_suggestion_task`（或复用 `_create_chat_suggestion_task` 逻辑）：在 `_create_chat_suggestion_task`（434）已有创建 waiting 任务逻辑，改为可被 `approval_pending` 复用（若 event 已含 script，用它建任务并返回 tid）。

> 实现要点：`ai_chat` 在 SSE 循环里收到 `approval_pending` 时，若事件无 task_id，调用 `_create_chat_suggestion_task(...)` 建任务、把返回的 tid 回填到 event（`event["task_id"] = tid`），再 `_format_sse`。

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile main.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/main.py
git commit -m "feat(orchestrator): capture approval_pending + attach task_id"
```

---

## Task 3: 前端 AIChat/ChatThread 渲染内联审批卡

**Files:**
- Modify: `aiops/observability-frontend/src/pages/AIChat/index.tsx`（dispatchEvent 171、渲染区）
- Modify: `aiops/observability-frontend/src/pages/AIChat/ChatThread.tsx`（dispatch 79-92、渲染区）
- Modify: `aiops/observability-frontend/src/api/client.ts`（`approveTask`/`rejectTask`）

**Interfaces:**
- Consumes: `approval_pending` 事件（Task 1-2）、现有 `/ops/tasks/{id}/approve|reject`。
- Produces: AIChat + ChatThread 渲染审批卡（task_id/plan/script/risk + 批准/拒绝按钮 → approveTask/rejectTask）。

- [ ] **Step 1: client.ts 封装审批**

```ts
export const approveTask = (id: string) => api.post(`/ops/tasks/${id}/approve`)
export const rejectTask = (id: string) => api.post(`/ops/tasks/${id}/reject`)
```

- [ ] **Step 2: AIChat dispatchEvent 补 approval_pending + 渲染审批卡**

```tsx
// AIChat/index.tsx 加 state
const [approval, setApproval] = useState<{ task_id: string; plan: string; script: string; risk_score: number; risk_reason: string } | null>(null)
// dispatchEvent 补 case
case 'approval_pending':
  setApproval({ task_id: ev.task_id, plan: ev.plan || '', script: ev.script || '', risk_score: ev.risk_score || 0, risk_reason: ev.risk_reason || '' })
  break
// 发送新消息时重置 setApproval(null)
// 渲染（在 toolCards 后）：
{approval && (
  <Card size="small" title="⏳ 待人工审批" style={{ background: 'var(--surface-2)', borderColor: '#d97706', borderRadius: 8, marginBottom: 12 }}>
    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 4 }}>{approval.plan} · 风险 {Math.round(approval.risk_score * 100)}%</div>
    <pre style={{ background: 'var(--surface)', padding: 8, borderRadius: 6, fontSize: 12, color: 'var(--text)', whiteSpace: 'pre-wrap' }}>{approval.script}</pre>
    <Space style={{ marginTop: 8 }}>
      <Button size="small" type="primary" onClick={() => { approveTask(approval.task_id).then(() => { message.success('已批准执行'); setApproval(null) }).catch(() => message.error('审批失败')) }}>批准执行</Button>
      <Button size="small" danger onClick={() => { rejectTask(approval.task_id).then(() => { message.success('已拒绝'); setApproval(null) }).catch(() => message.error('操作失败')) }}>拒绝</Button>
    </Space>
  </Card>
)}
```
> 需在 AIChat import `approveTask/rejectTask` 和 `Space`（若未 import）。

- [ ] **Step 3: ChatThread 同样补审批卡**

在 `ChatThread.tsx` 的 `dispatch` 加 `case 'approval_pending'`，并用同样的 state/渲染（可抽公共组件 `ApprovalCard` 或在两文件复制小段）。为简洁，两文件内联同样代码块。

- [ ] **Step 4: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/AIChat/index.tsx observability-frontend/src/pages/AIChat/ChatThread.tsx observability-frontend/src/api/client.ts
git commit -m "feat(frontend): inline approval card in AIChat + ChatThread"
```

---

## Task 4: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: Task 1-3。

- [ ] **Step 1: 重建镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t ai-orchestrator:latest ai-orchestrator && docker tag ai-orchestrator:latest docker.io/library/ai-orchestrator:latest && docker build -t observability-frontend:latest observability-frontend && docker tag observability-frontend:latest docker.io/library/observability-frontend:latest`
Expected: 构建成功（arm64）。

- [ ] **Step 2: 升级部署 + 滚动更新**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass" && kubectl -n observability rollout restart deploy/ai-orchestrator deploy/frontend`
Expected: deployed + 滚动更新。

- [ ] **Step 3: 验证 approval_pending 事件**

Run（登录 JWT，发起 chat 触发脚本提取）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
curl -s -N -X POST "http://localhost:30253/api/v1/ai/chat" -H "Content-Type: application/json" -H "Authorization: Bearer $JWT" -d '{"message":"获取 deepflow-server 服务信息并生成诊断建议","stream":true,"intent":"chat","service":"deepflow-server"}' --max-time 20 2>&1 | grep -A1 "event: approval_pending" | head -4
```
Expected: 若提取到脚本则输出 `event: approval_pending`（带 task_id）。若当前场景未提取脚本，则验证 `event: done` 正常返回（说明未破坏 chat 流）。

- [ ] **Step 4: 验证前端可访问**

Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/aichat`
Expected: 200。

- [ ] **Step 5: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix: deployment verification fixes" || echo "无改动"
```

- [ ] **Step 6: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功。
