# Phase B · ChatThread 会话线程页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 ongrid 式独立会话线程页 `/chat/:sessionId`（深链/刷新保留/回放历史），会话创建改为后端落库拿真实 id，并补 client.ts 会话 API 服务层。复用已完成的新 SSE 事件流（`event:` 帧 + tool_start/tool_end/done）。

**Architecture:** 前端。client.ts 封装会话 API（`getChatSessions`/`createSession`/`getSession`/`deleteSession`）；新增 `pages/AIChat/ChatThread.tsx`（`useParams` 取 sessionId，路由进入 `getMessages` 回放，复用 handleSend SSE 解析）；App.tsx 注册 `/chat/:sessionId`。

**Tech Stack:** React18 / react-router6 / axios / SSE。

## Global Constraints

- 前端：`observability-frontend/src/pages/AIChat/`（`index.tsx` 是现有 AIChat，会话管理已内联）、`src/api/client.ts`、`src/App.tsx`。
- **会话后端已具备**：`GET /ai/sessions`、`GET /ai/session/:sid`、`DELETE /ai/session/:sid`、`POST /ai/chat`（SSE，`session_id` 后端保存）。
- **现状缺口**：无 `/chat/:id` 路由；无 `createSession`/`getMessages` 服务函数；新建会话本地拼 id（不落库）；tool 卡片全局 state。
- 本次范围：**路由 + client 封装 + 新建落库 + 路由进入回放**。tool 卡片挂消息/渲染增强为可选后续（本次保持 toolCards 复用）。
- 合规：页面为自研功能，独立实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`90446d7`，每任务提交。

---

## Task 1: client.ts 封装会话 API 服务层

**Files:**
- Modify: `aiops/observability-frontend/src/api/client.ts`

**Interfaces:**
- Consumes: `api`（axios instance，已有）。
- Produces: `getChatSessions()`、`createSession()`、`getSession(sid)`、`deleteSession(sid)`。

- [ ] **Step 1: 加会话 API 封装**

```ts
// ===== Chat Sessions =====
export const getChatSessions = () => api.get('/ai/sessions')
export const createSession = () => api.post('/ai/session')
export const getSession = (sid: string) => api.get(`/ai/session/${sid}`)
export const deleteSession = (sid: string) => api.delete(`/ai/session/${sid}`)
```
> 注：`createSession` 的后端端点需确认——若 `POST /ai/session` 不存在，改用现有本地生成 id 的流程，仅封装读/删/回放。若后端有 `POST /ai/session` 创建接口则用；无则本 task 只加 get/delete/getSession，createSession 保持前端本地 id。

- [ ] **Step 2: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/api/client.ts
git commit -m "feat(frontend): chat session api service layer"
```

---

## Task 2: 新建 ChatThread 页组件（/chat/:sessionId）

**Files:**
- Create: `aiops/observability-frontend/src/pages/AIChat/ChatThread.tsx`
- Modify: `aiops/observability-frontend/src/App.tsx`（注册路由）
- Modify: `aiops/observability-frontend/src/pages/AIChat/index.tsx`（会话点击改为 navigate 到 /chat/:id）

**Interfaces:**
- Consumes: `getSession`（Task 1）、现有 handleSend/SSE 解析逻辑（从 index.tsx 复用）。
- Produces: `/chat/:sessionId` 独立线程页——进入时 `getSession(sid)` 回放消息；含输入框发送（复用 SSE 解析）、toolCards、progressText；会话标题显示 sessionId。

- [ ] **Step 1: 创建 ChatThread.tsx**

```tsx
// src/pages/AIChat/ChatThread.tsx
import React, { useEffect, useState, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Card, Input, Button, Spin, message } from 'antd'
import { SendOutlined } from '@ant-design/icons'
import { getSession, chatWithAI } from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

interface ChatMessage { id: string; role: 'user' | 'assistant'; content: string; timestamp?: string; thinking?: string }
interface ToolCard { tool_call_id: string; name: string; status: string; result?: string }

const ChatThread: React.FC = () => {
  const { sessionId } = useParams()
  const navigate = useNavigate()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [progressText, setProgressText] = useState('')
  const [toolCards, setToolCards] = useState<ToolCard[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)

  // 进入路由回放历史
  useEffect(() => {
    const load = async () => {
      if (!sessionId) { setHistoryLoading(false); return }
      try {
        const r = await getSession(sessionId)
        const msgs = (r?.data?.messages || []).map((m: any) => ({
          id: m.id || `${m.role}-${m.timestamp || Date.now()}`,
          role: m.role === 'user' ? 'user' : 'assistant',
          content: m.content || '',
          timestamp: m.timestamp,
        }))
        setMessages(msgs)
      } catch { message.error('加载会话失败') } finally { setHistoryLoading(false) }
    }
    load()
  }, [sessionId])

  const handleSend = async () => {
    const text = input.trim()
    if (!text || loading) return
    setInput(''); setLoading(true); setProgressText('分析开始...'); setToolCards([])
    const userMsg: ChatMessage = { id: `u-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }
    setMessages((p) => [...p, userMsg])
    try {
      const resp = await fetch('/api/v1/ai/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': 'default', Authorization: `Bearer ${localStorage.getItem('token') || ''}` },
        body: JSON.stringify({ message: text, stream: true, session_id: sessionId }),
      })
      const reader = resp.body?.getReader(); if (!reader) return
      const decoder = new TextDecoder(); let buf = ''; let fullText = ''
      let toolLocal: ToolCard[] = []
      const dispatch = (evName: string, ev: any) => {
        switch (evName) {
          case 'progress': if (ev.text) setProgressText(ev.text); break
          case 'chunk': if (ev.text) fullText += ev.text; break
          case 'assistant': fullText = ev.content ?? ev.text ?? fullText; break
          case 'tool_start': toolLocal.push({ tool_call_id: ev.tool_call_id, name: ev.name, status: 'pending' }); break
          case 'tool_end': toolLocal = toolLocal.map((t) => (t.tool_call_id === ev.tool_call_id ? { ...t, status: ev.status, result: ev.result } : t)); break
          case 'done': if (!fullText) fullText = ev.text ?? ev.assistant_message?.content ?? ''; break
          case 'error': fullText = `⚠️ ${ev.error ?? ev.text ?? ''}`; break
          default: break
        }
      }
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const frames = buf.split('\n\n'); buf = frames.pop() || ''
        for (const frame of frames) {
          if (!frame.trim()) continue
          let evName = 'message'; const dataLines: string[] = []
          for (const l of frame.split('\n')) {
            if (l.startsWith('event: ')) evName = l.slice(7).trim()
            else if (l.startsWith('data: ')) dataLines.push(l.slice(6))
          }
          if (dataLines.length === 0) continue
          try { dispatch(evName, JSON.parse(dataLines.join('\n'))) } catch {}
        }
      }
      setToolCards(toolLocal)
      if (fullText) setMessages((p) => [...p, { id: `a-${Date.now()}`, role: 'assistant', content: fullText, timestamp: new Date().toISOString() }])
    } catch { message.error('发送失败') } finally { setLoading(false); setProgressText('') }
  }

  if (historyLoading) return <Spin style={{ display: 'block', margin: '40px auto' }} />
  return (
    <Card title={`会话 #${sessionId}`} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10, height: 'calc(100vh - 140px)', display: 'flex', flexDirection: 'column' }} styles={{ body: { flex: 1, display: 'flex', flexDirection: 'column' } }}>
      <div style={{ flex: 1, overflowY: 'auto', marginBottom: 16 }}>
        {messages.map((m) => (
          <div key={m.id} style={{ display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start', marginBottom: 12 }}>
            <div style={{ maxWidth: '72%', padding: '10px 14px', borderRadius: 12, background: m.role === 'user' ? '#2563eb' : 'var(--surface-2)', color: m.role === 'user' ? '#fff' : 'var(--text)', whiteSpace: 'pre-wrap' }}>
              {m.content}
            </div>
          </div>
        ))}
        {toolCards.map((t) => (
          <div key={t.tool_call_id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px', marginBottom: 6, background: 'var(--surface-2)', borderRadius: 8 }}>
            <span style={{ fontSize: 12, color: 'var(--text)' }}>⚙️ {t.name}</span>
            <span style={{ fontSize: 11, color: t.status === 'success' ? '#22c55e' : '#a1a1aa' }}>{t.status}</span>
          </div>
        ))}
        {loading && <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>🤖 {progressText}</div>}
      </div>
      <div style={{ display: 'flex', gap: 8 }}>
        <Input.TextArea value={input} onChange={(e) => setInput(e.target.value)} placeholder="输入消息..." autoSize={{ minRows: 1, maxRows: 4 }} onPressEnter={(e) => { if (!e.shiftKey) { e.preventDefault(); handleSend() } }} />
        <Button type="primary" icon={<SendOutlined />} onClick={handleSend} loading={loading}>发送</Button>
      </div>
    </Card>
  )
}
export default ChatThread
```
> 注：`chatWithAI` import 可能未使用（用裸 fetch SSE），移除未用 import 避免 lint。

- [ ] **Step 2: App.tsx 注册路由**

```tsx
import ChatThread from './pages/AIChat/ChatThread'
<Route path="/chat/:sessionId" element={<ChatThread />} />
```

- [ ] **Step 3: AIChat/index.tsx 会话点击改为 navigate**

将 `loadSession(sid)` 点击改为 `navigate(`/chat/${sid}`)`：
```tsx
// 会话列表项 onClick 处
onClick={() => navigate(`/chat/${item.session_id}`)}
```
需在 AIChat 加 `useNavigate`。

- [ ] **Step 4: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/AIChat/ChatThread.tsx observability-frontend/src/pages/AIChat/index.tsx observability-frontend/src/App.tsx
git commit -m "feat(frontend): chat thread page /chat/:sessionId with history replay"
```

---

## Task 3: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: Task 1-2。

- [ ] **Step 1: 重建前端镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t observability-frontend:latest observability-frontend && docker tag observability-frontend:latest docker.io/library/observability-frontend:latest`
Expected: 构建成功（arm64）。

- [ ] **Step 2: 升级部署 + 滚动更新**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass" && kubectl -n observability rollout restart deploy/frontend`
Expected: deployed + frontend 重启。

- [ ] **Step 3: 验证前端 + 新路由**

Run: `sleep 30 && curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/`
Expected: 200。
Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/chat/test-session`
Expected: 200（ChatThread 路由注册，加载会话）。

- [ ] **Step 4: 验证会话接口**

Run（登录 JWT）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
curl -s "http://localhost:30253/api/v1/ai/sessions" -H "Authorization: Bearer $JWT" | head -c 200
```
Expected: 返回 `{"sessions":[...]}` 或空列表。

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
