# Phase B · SSE 事件协议重构（帧格式 + 前端渲染框架）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将自研 AI 聊天 SSE 协议对齐 ongrid 事件模型（`event:` 帧 + 结构化 `assistant`/`tool_start`/`tool_end`/`approval_pending`/`done`/`error`），并升级前端 AIChat 为按事件渲染（工具卡片/审批卡/assistant 消息）。这是 ChatThread 及 agent 型页面的共同地基。

**Architecture:** 后端 `orchestrator.py stream_sync` + `main.py ai_chat` 输出标准 SSE `event:` 帧并增加结构化事件；前端 AIChat 改为按 `\n\n` 空行分帧、解析 `event:` 行、按事件分派渲染。本次聚焦**帧格式与前端渲染框架**（tool_start/tool_end 数据源当前来自节点级推断，真实工具级采集作为独立后续）。

**Tech Stack:** Python FastAPI / React18 / fetch ReadableStream / SSE。

## Global Constraints

- 后端：`ai-orchestrator/orchestrator.py`（`stream_sync` 772 行）、`main.py`（`ai_chat` 131 行 + `generate` 163 行）。
- 前端：`observability-frontend/src/pages/AIChat/index.tsx`（SSE 解析 147-179 行、渲染 321-430 行）。
- **当前帧格式**：仅 `data:` 行，类型在 JSON `type` 字段（`progress`/`chunk`/`suggestion`/`done`/`error`）。
- **目标格式**：标准 SSE `event: <type>\ndata: <json>\n\n`；事件含 `assistant`/`tool_start`/`tool_end`/`approval_pending`/`done`/`error`（保留 `progress`/`chunk`/`suggestion` 作为兼容）。
- 合规：SSE 为通用流式协议（Server-Sent Events 标准），事件命名对 ongrid 为通用 agent 模式，独立实现，不复刻其代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`547877e`，每任务提交。

---

## Task 1: 后端输出标准 SSE event 帧 + 结构化 done/error

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`（`generate` 163 行）

**Interfaces:**
- Consumes: `orchestrator.stream_sync()` 的现有 `yield {"type": ...}` 事件。
- Produces: `generate()` 将每个事件输出为标准 SSE 帧：`event: {type}\ndata: {json}\n\n`；`done`/`error` 补结构化字段（`code`/`assistant_message`）。

- [ ] **Step 1: 修改 generate() 输出 SSE event 帧**

```python
# main.py generate() 内，事件转发处（现 line ~174）
def _format_sse(ev: dict) -> str:
    """将内部事件 dict 序列化为标准 SSE 帧。"""
    etype = ev.get("type", "message")
    data = json.dumps(ev, ensure_ascii=False)
    return f"event: {etype}\ndata: {data}\n\n"
```
在 `generate()` 循环内（`yield f"data: {json.dumps(event)}\n\n"` 处）改为：
```python
yield _format_sse(event)
```
`done` 事件补 `assistant_message` 结构（`orchestrator.stream_sync` 的 done 只有 text，此处封装）：
```python
if event.get("type") == "done":
    yield _format_sse({"type": "done", "text": event.get("text", ""),
                        "assistant_message": {"id": f"asst_{thread_id}", "content": event.get("text", ""), "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ")}})
elif event.get("type") == "error":
    yield _format_sse({"type": "error", "error": event.get("text", ""), "code": "dag_error"})
else:
    yield _format_sse(event)
```

- [ ] **Step 2: 语法 + 手动冒烟**

Run: `cd aiops/ai-orchestrator && python3 -c "import main"` （或 `python3 -m py_compile main.py`）
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/main.py
git commit -m "feat(orchestrator): SSE event: frames + structured done/error"
```

---

## Task 2: 前端 AIChat 按 \n\n 分帧 + 解析 event 行

**Files:**
- Modify: `aiops/observability-frontend/src/pages/AIChat/index.tsx`（147-179 行）

**Interfaces:**
- Consumes: 后端 `event:` 帧（Task 1）。
- Produces: 按 `\n\n` 空行分帧；每帧解析 `event:` 与 `data:` 行；按事件名分派。

- [ ] **Step 1: 修改 SSE 解析为按空行分帧**

```tsx
// 替换现有 buf.split('\n') 逻辑（index.tsx ~163）
const processFrame = (frame: string) => {
  if (!frame.trim()) return
  const lines = frame.split('\n')
  let evName = 'message'
  const dataLines: string[] = []
  for (const l of lines) {
    if (l.startsWith('event: ')) evName = l.slice(7).trim()
    else if (l.startsWith('data: ')) dataLines.push(l.slice(6))
  }
  if (dataLines.length === 0) return
  const payload = JSON.parse(dataLines.join('\n'))
  dispatchEvent(evName, payload)
}
// 读取循环中：按 '\n\n' 切帧
let buf = ''
const decoder = new TextDecoder()
// 每块 chunk：buf += decoder.decode(chunk, {stream:true}); 按 '\n\n' 切出完整帧处理
```

- [ ] **Step 2: 增加 dispatchEvent 分派（含新事件类型分支）**

```tsx
const dispatchEvent = (evName: string, ev: any) => {
  switch (evName) {
    case 'progress': setProgressText(ev.text || ''); break
    case 'chunk': setFullText(prev => prev + (ev.text || '')); break
    case 'assistant': setFullText(ev.content ?? ev.text ?? ''); break
    case 'tool_start': /* 预留: 加入 toolCards, 本次优雅降级 */ break
    case 'tool_end': /* 预留 */ break
    case 'approval_pending': /* 预留: 设置等待审批胶囊 */ setWaitingApproval(true); break
    case 'done':
      if (!fullTextRef.current) setFullText(ev.text ?? ev.assistant_message?.content ?? '')
      setWaitingApproval(false)
      break
    case 'error': setFullText(prev => prev + `⚠️ ${ev.error ?? ev.text ?? ''}`); setWaitingApproval(false); break
    default: break
  }
}
```
> 注：`fullText` 在闭包中的最新值需用 ref（`fullTextRef`）或函数式更新，避免陈旧闭包。`tool_start/tool_end` 事件本次仅预留分支（数据源为节点级推断，见 Task 3），不渲染卡片但不清空。

- [ ] **Step 3: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/AIChat/index.tsx
git commit -m "feat(frontend): AIChat SSE event: frame parsing + event dispatch"
```

---

## Task 3: 工具调用事件（节点级推断）+ 前端工具卡片渲染

**Files:**
- Modify: `aiops/ai-orchestrator/orchestrator.py`（`stream_sync` 772 行，节点迭代处）
- Modify: `aiops/observability-frontend/src/pages/AIChat/index.tsx`（渲染工具卡片）

**Interfaces:**
- Consumes: `graph.stream` 节点数据（`node_data` 含 `crewai_result`/`holmesgpt_result` 等）。
- Produces: 在关键节点（crewai/holmes/rca/rag）执行时，`stream_sync` 发合成 `tool_start`/`tool_end` 事件（tool_name 取自节点名，arguments 取自 node_data 摘要）；前端渲染 `kind:'tool_card'` 工具卡片（status 胶囊）。

- [ ] **Step 1: stream_sync 发合成 tool 事件**

在 `stream_sync` 节点迭代处（line 800 `yield progress` 后）加：
```python
# 工具级事件（节点级推断；真实工具级采集为独立后续）
tool_node_map = {"crewai": "CrewAI 分析", "holmes": "Trace 调查", "rca": "RCA 根因分析", "rag": "RAG 案例匹配", "plan": "生成操作方案"}
tool_start_id = f"tool_{node_name}_{step_num}"
if node_name in tool_node_map:
    yield {"type": "tool_start", "tool_call_id": tool_start_id, "name": tool_node_map[node_name],
           "status": "pending", "arguments": {}}
# ...(节点执行后，同次迭代末尾)...
yield {"type": "tool_end", "tool_call_id": tool_start_id, "name": tool_node_map.get(node_name, node_name),
       "status": "success", "arguments": {}, "result": str(node_data)[:500]}
```
> 说明：由于 `graph.stream` 每步是"节点输入+输出"迭代，`tool_start`/`tool_end` 会相邻出现。为真实语义，可将 `tool_start` 放节点执行前的上一个迭代标记（如需精确时序可后续优化）；本次以"每节点产生一次 tool 事件对"为目标，确保前端能渲染卡片。

- [ ] **Step 2: 前端渲染工具卡片**

```tsx
// AIChat 消息列表渲染区，新增 tool 卡片
const [toolCards, setToolCards] = useState<Array<{tool_call_id:string,name:string,status:string,result?:string}>>([])
// dispatchEvent tool_start: setToolCards(prev => [...prev, {tool_call_id, name, status:'pending'}])
// dispatchEvent tool_end: setToolCards(prev => prev.map(t => t.tool_call_id===ev.tool_call_id ? {...t, status:ev.status, result:ev.result} : t))
```
渲染（在消息气泡区顶部或按顺序）：
```tsx
{toolCards.map((t) => (
  <div key={t.tool_call_id} style={{ display:'flex', alignItems:'center', gap:8, padding:'6px 12px', marginBottom:6, background:'var(--surface-2)', borderRadius:8 }}>
    <span style={{ fontSize:12 }}>⚙️ {t.name}</span>
    <span style={{ fontSize:11, color: t.status==='success' ? '#22c55e' : t.status==='pending' ? '#a1a1aa' : '#ef4444' }}>{t.status}</span>
    {t.result && <span style={{ fontSize:10, color:'var(--text-muted)' }}>{String(t.result).slice(0,80)}</span>}
  </div>
))}
```

- [ ] **Step 3: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/orchestrator.py observability-frontend/src/pages/AIChat/index.tsx
git commit -m "feat: tool_start/tool_end events (node-level) + tool card rendering"
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

- [ ] **Step 3: 验证 SSE 事件帧**

Run（登录 JWT + 调 chat stream=true，观察 event: 帧）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
curl -s -N -X POST "http://localhost:30253/api/v1/ai/chat" -H "Content-Type: application/json" -H "Authorization: Bearer $JWT" -d '{"message":"诊断服务异常","stream":true,"intent":"chat"}' 2>&1 | head -c 600
```
Expected: 输出含 `event: progress`/`event: tool_start`/`event: done` 等 SSE 帧（`event:` + `data:` 行）。

- [ ] **Step 4: 验证前端可访问**

Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/`
Expected: 200。
Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/aichat`
Expected: 200（AIChat 页）。

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
