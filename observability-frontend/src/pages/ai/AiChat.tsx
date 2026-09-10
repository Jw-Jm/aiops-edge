import React, { useEffect, useState, useRef } from 'react'
import { Button, Input, Empty, Alert, Drawer, message } from 'antd'
import { BookOutlined, ExperimentOutlined } from '@ant-design/icons'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import api, { getSession, addKnowledgeCase } from '../../api/client'
import { useSearchParams, useNavigate } from 'react-router-dom'
import AppIcon from '../../components/AppIcons'
import { useScopeStore } from '../../store/scopeStore'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import { createInvestigationDraft, parseAssistantAnswer, type AssistantAnswer, type AssistantConversationScope } from '../../api/assistant'
import AssistantAnswerCard from '../Assistant/AssistantAnswerCard'
import AssistantContextPanel from '../Assistant/AssistantContextPanel'

// canonical UUID 校验（与 Query API AuthMiddleware canonicalUUID 一致），用于判断
// 是否已选择 concrete cluster（F-07 / A0-04：拒绝把 'all' 当可发送的 cluster）。
const CANONICAL_UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

interface ChatMessage {
  id: string; role: 'user' | 'assistant'; content: string; timestamp: string
  kind?: 'text' | 'suggestion' | 'execresult' | 'report' | 'answer'
  plan?: string; script?: string; threadId?: string; riskScore?: number; riskReason?: string; service?: string
  answer?: AssistantAnswer; turnId?: string
}

interface ToolActivity {
  id: string
  name: string
  status: 'running' | 'success' | 'error' | 'unavailable'
  result?: string
}

// P1-5: 兼容新旧两种风险格式：
// - 新格式 risk_score 0~1 → 显示 1-5 星（"风险等级: ★×N/5"）
// - 旧格式 risk_score 0~100 / risk_reason 文本 → 原样展示
function riskView(m: ChatMessage): { text: string; color: string } | null {
  const s = m.riskScore
  if (typeof s === 'number' && !isNaN(s) && s >= 0 && s <= 1) {
    const stars = Math.max(1, Math.min(5, Math.round(s * 5)))
    return {
      text: `风险等级: ★×${stars}/5`,
      color: stars >= 4 ? 'var(--danger)' : stars >= 2 ? 'var(--warning)' : 'var(--success)',
    }
  }
  if (typeof s === 'number' && !isNaN(s) && s > 1) {
    return {
      text: `风险评分: ${s}/100${m.riskReason ? `（${m.riskReason}）` : ''}`,
      color: s > 60 ? 'var(--danger)' : s > 30 ? 'var(--warning)' : 'var(--success)',
    }
  }
  if (m.riskReason) return { text: `风险等级: ${m.riskReason}`, color: 'var(--warning)' }
  return null
}

function freezeAssistantScope(clusterId: string, activeScope: ReturnType<typeof useScopeStore.getState>['active']): AssistantConversationScope {
  const now = new Date()
  const end = activeScope.timeRange.mode === 'absolute' ? activeScope.timeRange.end : now.toISOString()
  const start = activeScope.timeRange.mode === 'absolute'
    ? activeScope.timeRange.start
    : new Date(now.getTime() - activeScope.timeRange.minutes * 60_000).toISOString()
  return {
    clusterId,
    ...(activeScope.resource?.uid ? { resourceUid: activeScope.resource.uid } : {}),
    from: start,
    to: end,
    knowledgeScope: 'platform_common_and_current_cluster',
  }
}

const AiChat: React.FC = () => {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const [sessions, setSessions] = useState<any[]>([])
  const [activeSession, setActiveSession] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState(searchParams.get('q') || '')
  const [loading, setLoading] = useState(false)
  // A0-04（F-07）：concrete cluster 来自 UIStore（与 ClusterSwitcher 一致），
  // 不依赖 localStorage 手解；无 concrete cluster（'all'/空）时禁用发送。
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const activeScope = useScopeStore((s) => s.active)
  const hasConcreteCluster = !!activeClusterId && CANONICAL_UUID_RE.test(activeClusterId)
  const [progress, setProgress] = useState('')
  const [toolActivity, setToolActivity] = useState<ToolActivity[]>([])
  const [frozenScope, setFrozenScope] = useState<AssistantConversationScope>()
  const [retryableTurn, setRetryableTurn] = useState<{ turnId: string; text: string; execResult?: string }>()
  const [mobileSessionOpen, setMobileSessionOpen] = useState(false)
  const [mobileContextOpen, setMobileContextOpen] = useState(false)
  // P0-1: 后端 SSE notice 事件（type=notice, level=warning, text=...）→ 消息区顶部黄色提示条
  const [notice, setNotice] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  // 需求：对话沉淀故障案例——caseAdding: 防重 loading；caseAdded: 记录已入库的消息，避免重复提交
  const [caseAdding, setCaseAdding] = useState<Record<string, boolean>>({})
  const caseAddedRef = useRef<Set<string>>(new Set())
  const turnRef = useRef<{ turnId: string; text: string; execResult?: string } | null>(null)

  const loadSessions = async () => {
    try { const r = await api.get('/ai/sessions'); setSessions(r.data?.sessions || []) }
    catch { setNotice('会话列表加载失败，请稍后重试') }
  }
  // Issue4: 清除单个会话（checkpoints + session_store）
  const clearOne = async (e: React.MouseEvent, sid: string) => {
    e.stopPropagation()
    if (!window.confirm('确认删除该会话？')) return
    try { await api.delete(`/ai/session/${sid}`) }
    catch { message.error('会话删除失败'); return }
    setSessions((p) => p.filter((s) => s.session_id !== sid))
    if (activeSession === sid) { setActiveSession(''); setMessages([]) }
  }
  // Issue4: 清除全部会话
  const clearAll = async () => {
    if (!window.confirm(`确认清空全部 ${sessions.length} 个历史会话？`)) return
    try { await api.delete('/ai/sessions') }
    catch { message.error('会话清空失败'); return }
    setSessions([]); setActiveSession(''); setMessages([])
  }
  // 修复(P2-1)：会话标题清理。preview 是用户首条消息原文，可能含 markdown 标记
  // （###、```等），且过长。这里去掉 markdown 符号并截短到 28 字符。
  const fmtTitle = (s: any) => {
    let t = s.preview || s.session_id?.slice(0, 20) || ''
    t = String(t)
      .replace(/^#{1,6}\s+/g, '')           // 去掉行首 # 标题标记
      .replace(/`{1,3}/g, '')                // 去掉反引号
      .replace(/\*\*/g, '')                // 去掉 ** 粗体标记
      .replace(/\*+/g, '')                  // 去掉 * 斜体标记
      .replace(/[\r\n]+/g, ' ')            // 换行转空格
      .trim()
    return t.length > 28 ? t.slice(0, 28) + '…' : t
  }
  const fmtTime = (ts: any) => {
    if (!ts) return ''
    const n = typeof ts === 'number' ? ts * 1000 : (typeof ts === 'string' && !isNaN(Date.parse(ts)) ? Date.parse(ts) : 0)
    if (!n) return ''
    return new Date(n).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  }
  const loadSession = async (sid: string) => {
    try {
      const d = (await getSession(sid)).data
      const msgs: ChatMessage[] = []
      // B2 修复：保留服务端返回的完整消息对象（kind/plan/script/threadId/riskScore/riskReason/service 等
      // 处置卡片元数据原样还原），仅兜底 id/timestamp；消息 id 优先用服务端唯一 id。
      ;(d?.messages || []).forEach((m: any, i: number) => {
        const base = { ...m, id: m.id || `s-${sid}-${i}`, timestamp: m.timestamp || d.created_at || '' }
        if (m.role === 'user') msgs.push({ ...base, role: 'user', content: m.content })
        else if (m.role === 'assistant') msgs.push({ ...base, role: 'assistant', content: m.content })
      })
      if (d?.scope?.cluster_id && d?.scope?.from && d?.scope?.to) {
        setFrozenScope({ clusterId: d.scope.cluster_id, resourceUid: d.scope.resource_uid || undefined, from: d.scope.from, to: d.scope.to, knowledgeScope: 'platform_common_and_current_cluster' })
      }
      setMessages(msgs); setActiveSession(sid)
    } catch { setNotice('会话加载失败，请稍后重试') }
  }

  useEffect(() => { loadSessions() }, [])
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [messages, progress])

  // P3-2 修复: 从 AiDock/快捷入口携带 ?q= 进入时，自动发送该问题（无需手动点发送）
  const autoSentRef = useRef(false)
  useEffect(() => {
    const q = searchParams.get('q')
    if (q && !autoSentRef.current) {
      autoSentRef.current = true
      handleSend(q)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const newSession = () => { setMessages([]); setActiveSession(''); setFrozenScope(undefined); setRetryableTurn(undefined); turnRef.current = null; setInput('') }

  // 兼容旧版多轮上下文；动作执行仍必须走显式调查与审批链路。
  const handleSend = async (preset?: string, execResult?: string, retry = false) => {
    const text = (preset ?? input).trim()
    if (!text && !execResult) return
    if (loading) return
    const sessionId = activeSession
    // Stable per-submission identity: a transport retry can reuse this turn
    // and Query API will converge transcript inserts instead of duplicating
    // the user message.
    const turnId = retry ? (turnRef.current?.turnId || crypto.randomUUID()) : crypto.randomUUID()
    turnRef.current = { turnId, text, execResult }
    const requestScope = frozenScope || freezeAssistantScope(activeClusterId, activeScope)
    if (!frozenScope) setFrozenScope(requestScope)
    setInput('')
    if (text) setMessages((prev) => [...prev, { id: `u-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }])
    setLoading(true); setRetryableTurn(undefined); setProgress('收集事实')
    setToolActivity([])
    const controller = new AbortController()
    abortRef.current = controller
    try {
      // A0-04（F-07）：移除默认 cluster_id=all。Query API ProxyChat 要求 concrete
      // canonical cluster，'all' 会被 fail-closed 拒绝。未选择 concrete cluster 时
      // 不发送，提示用户先选择集群（ClusterSwitcher）。
      if (!hasConcreteCluster) {
        message.warning('请先在顶部集群选择器选择具体集群后，再发起 AI 对话')
        return
      }
      const clusterId = activeClusterId
      // B12 修复：统一走共享 api 实例（复用其 baseURL / token / 拦截器逻辑），
      // SSE 流式响应保留 fetch 实现（axios 不便于流式读取）。
      const resp = await fetch(`${api.defaults.baseURL}/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          intent: 'diagnosis', service: '', message: text, stream: true, session_id: sessionId, turn_id: turnId,
          cluster_id: clusterId, environment: 'prod', namespace: activeScope.resource?.domain === 'kubernetes' ? activeScope.resource.namespace : undefined,
           resource_id: requestScope.resourceUid || '', resource_type: activeScope.resource?.type || '',
           time_range: { mode: 'absolute', start: requestScope.from, end: requestScope.to }, knowledge_scope: requestScope.knowledgeScope, exec_result: execResult || '',
        }),
        signal: controller.signal,
      })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const reader = resp.body?.getReader(); if (!reader) throw new Error('No stream')
      const decoder = new TextDecoder(); let buf = ''; let fullText = ''
      const pendingSuggestions: any[] = []
      let structuredAnswer: AssistantAnswer | null = null
      while (true) {
        const { done, value } = await reader.read(); if (done) break
        buf += decoder.decode(value, { stream: true })
        const frames = buf.split('\n\n'); buf = frames.pop() || ''
        for (const frame of frames) {
          if (!frame.trim()) continue
          let evName = 'message'; const dataLines: string[] = []
          for (const l of frame.split('\n')) {
            if (l.startsWith('event: ')) evName = l.slice(7).trim()
            else if (l.startsWith('data: ')) dataLines.push(l.slice(6))
          }
          if (!dataLines.length) continue
          try {
            const ev = JSON.parse(dataLines.join('\n'))
            if (evName === 'progress' && ev.text) setProgress(String(ev.text))
            else if (evName === 'chunk' && ev.text) fullText += ev.text
            else if (evName === 'assistant') fullText = ev.content ?? ev.text ?? fullText
            else if (evName === 'answer' || evName === 'assistant_answer') structuredAnswer = parseAssistantAnswer(ev)
            else if (evName === 'done') {
              structuredAnswer = structuredAnswer || parseAssistantAnswer(ev)
              if (!fullText) fullText = ev.text ?? ev.assistant_message?.content ?? structuredAnswer?.conclusion ?? ''
            }
            else if (evName === 'error') fullText = `⚠️ ${ev.error ?? ev.text ?? ''}`
            else if (evName === 'notice') {
              // P0-1: notice 事件 → 消息区顶部黄色提示条（用户可关闭）
              const text = ev.text ?? ev.message ?? ev.content ?? ''
              if (text) setNotice(String(text))
            }
            else if (evName === 'tool_start') {
              const id = String(ev.tool_call_id || `${ev.name}-${Date.now()}`)
              setToolActivity((prev) => {
                if (prev.some((x) => x.id === id)) return prev
                return [...prev, { id, name: String(ev.name || '工具'), status: 'running' }]
              })
            }
            else if (evName === 'tool_end') {
              const id = String(ev.tool_call_id || '')
              const status = ev.status === 'error'
                ? 'error'
                : ev.status === 'unavailable'
                  ? 'unavailable'
                  : 'success'
              setToolActivity((prev) => prev.map((x) => x.id === id
                ? { ...x, name: String(ev.name || x.name), status, result: String(ev.result || '') }
                : x))
            }
            else if (evName === 'suggestion') {
              // Issue1: 每次分析只渲染一张处置建议确认卡。后端每次分析只发一个 suggestion
              // 事件（已去重），此处仅收集 suggestion 类型，避免把 approval_pending 也当一张卡
              // 导致"多个内容一致的处置建议·待确认"。按 threadId 去重（同轮只一张）。
              const tid = ev.thread_id ?? ev.task_id ?? sessionId
              if (!pendingSuggestions.some((s: any) => s.threadId === tid)) {
                pendingSuggestions.push({
                  plan: ev.plan ?? ev.text ?? '', script: ev.script ?? '', threadId: tid,
                  riskScore: ev.risk_score ?? 0, riskReason: ev.risk_reason ?? ev.risk ?? '',
                })
              }
            }
          } catch { setNotice('服务返回了无法解析的事件') }
        }
      }
      const aiText = fullText || structuredAnswer?.conclusion || 'LLM 分析未返回结果，请检查配置后重试。'
      const newMsgs: ChatMessage[] = [{ id: `a-${Date.now()}`, role: 'assistant', content: aiText, kind: structuredAnswer ? 'answer' : 'text', answer: structuredAnswer || undefined, turnId, timestamp: new Date().toISOString() }]
      pendingSuggestions.forEach((s, idx) => {
        newMsgs.push({ id: `sugg-${Date.now()}-${idx}`, role: 'assistant', content: '', kind: 'suggestion',
          plan: s.plan, script: s.script, threadId: s.threadId, riskScore: s.riskScore, riskReason: s.riskReason, timestamp: new Date().toISOString() })
      })
      setMessages((prev) => [...prev, ...newMsgs])
      const responseSessionId = resp.headers.get('X-Session-Id') || resp.headers.get('X-Session-ID') || ''
      if (fullText || pendingSuggestions.length) {
        // The server is the session-id authority. Persist the response value
        // immediately so a first turn followed by refresh remains multi-turn.
        setActiveSession(responseSessionId || sessionId)
        loadSessions()
      }
      setRetryableTurn(undefined)
    } catch (err: any) {
      const msg = err?.name === 'AbortError' ? '⏱️ 已中断 / 超时 (120s)' : `❌ 请求失败：${err?.message || ''}`
      setMessages((prev) => [...prev, { id: `e-${Date.now()}`, role: 'assistant', content: msg, timestamp: new Date().toISOString() }])
      setRetryableTurn({ turnId, text, execResult })
    } finally {
      setLoading(false); setProgress(''); abortRef.current = null
    }
  }

  const handleCreateInvestigationDraft = async (request: AssistantConversationScope & { status: 'draft' }) => {
    try {
      await createInvestigationDraft({ clusterId: request.clusterId, resourceUid: request.resourceUid, scope: request, status: 'draft' })
      const query = new URLSearchParams({ source: 'assistant', clusterId: request.clusterId, timeStart: request.from, timeEnd: request.to })
      if (request.resourceUid) query.set('resource', request.resourceUid)
      navigate(`/investigation/new?${query.toString()}`)
    } catch {
      message.error('调查草稿创建失败，请稍后重试')
    }
  }

  // 兼容旧版对话沉淀：将已完成分析写入待审运维知识（POST /ai/knowledge/case）
  // payload: service=当前会话服务; symptom=用户问题; root_cause=分析摘要; plan=处置命令/建议
  const handleAddCase = async (m: ChatMessage, symptom: string) => {
    if (caseAdding[m.id] || caseAddedRef.current.has(m.id)) return
    setCaseAdding((p) => ({ ...p, [m.id]: true }))
    const lastUser = [...messages].reverse().find((x) => x.role === 'user')
    const userText = symptom || lastUser?.content || ''
    // root_cause：普通助手回复本身即分析内容；处置建议卡则取其之前最近一条 AI 分析（均截 500 字）
    let analysis = m.content
    if (m.kind !== 'text' && m.kind !== 'report') {
      const idx = messages.findIndex((x) => x.id === m.id)
      const analysisMsg = [...messages.slice(0, idx === -1 ? messages.length : idx)].reverse().find(
        (x) => x.role === 'assistant' && x.kind !== 'suggestion' && x.kind !== 'execresult' && x.kind !== 'report'
      )
      analysis = analysisMsg?.content || m.content
    }
    const plan = (m.script || m.plan || '').slice(0, 500)
    addKnowledgeCase({
      service: m.service || '',
      symptom: userText.slice(0, 500),
      root_cause: analysis.slice(0, 500),
      plan,
    })
      .then((res: any) => {
        const d = res.data || {}
        caseAddedRef.current.add(m.id)
        setCaseAdding((p) => ({ ...p, [m.id]: false }))
        if (d.inserted === false) message.warning(d.message || '已存在相似案例，未重复加入')
        else message.success(d.case_id ? `已加入知识库 (案例 ${d.case_id})` : '已加入知识库')
      })
      .catch((e: any) => {
        setCaseAdding((p) => ({ ...p, [m.id]: false }))
        const err = e?.response?.data?.error || e?.response?.data?.detail || e?.message || '加入失败'
        message.error(`质量审查未通过：${err}`)
      })
  }

  return (
    <div className="assistant-workspace" style={{ height: 'calc(100vh - 116px)' }}>
      {/* 会话列表 */}
      <div className="card assistant-session-pane" style={{ width: 220, flexShrink: 0, marginBottom: 0, display: 'flex', flexDirection: 'column' }}>
        <div className="card__head"><span className="card__title">会话</span>
          <span>
            <Button size="small" type="primary" onClick={newSession}>新对话</Button>
            {sessions.length > 0 && <Button size="small" danger style={{ marginLeft: 6 }} onClick={clearAll}>清空</Button>}
          </span>
        </div>
        <div style={{ flex: 1, overflow: 'auto', padding: 8 }}>
          {sessions.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无会话" />}
          {sessions.map((s) => (
            <div key={s.session_id} onClick={() => loadSession(s.session_id)}
              style={{ padding: '8px 10px', borderRadius: 8, cursor: 'pointer', marginBottom: 2, background: activeSession === s.session_id ? 'var(--primary-soft)' : 'transparent', position: 'relative' }}>
              <div style={{ fontSize: 12, paddingRight: 16 }}>{fmtTitle(s)}</div>
              <div style={{ fontSize: 10, color: 'var(--text-muted)', display: 'flex', justifyContent: 'space-between', marginTop: 2 }}>
                <span>{s.intent || ''}</span><span>{fmtTime(s.created_at)}</span>
              </div>
              <Button size="small" type="text" danger onClick={(e) => clearOne(e, s.session_id)}
                style={{ position: 'absolute', right: 2, top: 2, padding: 0, width: 20, height: 20, fontSize: 12 }} title="删除会话">✕</Button>
            </div>
          ))}
        </div>
      </div>

      {/* 主聊天区 */}
      <div className="card assistant-main-pane" style={{ flex: 1, marginBottom: 0, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <div className="card__head">
          <span className="card__title">AI 运维助手</span>
          <span className="assistant-mobile-actions"><Button size="small" className="assistant-mobile-button" onClick={() => setMobileSessionOpen(true)}>会话</Button><Button size="small" className="assistant-mobile-button" onClick={() => setMobileContextOpen(true)}>Scope</Button></span>
          <span className="scope-chip" aria-label="Chat 活动资源">
            {activeScope.resource
              ? `${resourceTypeLabel(activeScope.resource.type)} · ${resourceLocation(activeScope.resource)}`
             : '集群范围'}
          </span>
        </div>
        <div className="assistant-stage-bar" aria-label="回答阶段">
          {['收集事实', '检索知识', '组织回答'].map((stage) => <span key={stage} className={progress === stage ? 'is-active' : ''}>{stage}</span>)}
        </div>
        <div style={{ flex: 1, overflow: 'auto', padding: 16 }}>
          {messages.length === 0 && (
            <div style={{ textAlign: 'center', padding: '40px 20px' }}>
              <div style={{ fontSize: 18, fontWeight: 700, marginBottom: 8 }}>AI 运维助手</div>
              <div style={{ fontSize: 13, color: 'var(--text-muted)', marginBottom: 20 }}>用自然语言描述问题，我会自动分析指标、日志、链路与告警。</div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', justifyContent: 'center' }}>
                <Button onClick={() => handleSend('分析 prod 集群故障根因')}>分析集群根因</Button>
                <Button onClick={() => handleSend('巡检所有 K8s 集群')}>集群巡检</Button>
                <Button onClick={() => handleSend('为什么 order-svc 延迟升高')}>服务延迟排查</Button>
              </div>
            </div>
          )}
          {/* P0-1: SSE notice 事件提示条（黄色警告，可关闭） */}
          {notice && (
            <Alert type="warning" showIcon closable message={notice}
              onClose={() => setNotice('')} style={{ marginBottom: 12 }} />
          )}
          {toolActivity.length > 0 && (
            <div style={{ marginBottom: 12, padding: '8px 10px', borderRadius: 8, background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
              {toolActivity.map((tool) => (
                <div key={tool.id} style={{ fontSize: 12, lineHeight: 1.6 }}>
                  <span style={{ marginRight: 6 }}>{tool.status === 'running' ? '🔧' : tool.status === 'success' ? '✅' : tool.status === 'unavailable' ? '⚠️' : '❌'}</span>
                  <b>{tool.name}</b>{tool.status === 'running' ? ' 运行中…' : tool.status === 'success' ? ' 已完成' : tool.status === 'unavailable' ? ' 不可用' : ' 失败'}
                  {tool.result && <div style={{ margin: '3px 0 4px 22px', color: 'var(--text-muted)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{tool.result}</div>}
                </div>
              ))}
            </div>
          )}
          {messages.map((m, i) => {
            // 需求：沉淀故障案例时取当前建议之前最近一条用户问题作为 symptom
            const symptomMsg = [...messages.slice(0, i)].reverse().find((x) => x.role === 'user')
            return (
            <div key={m.id} style={{ display: 'flex', gap: 10, marginBottom: 14, justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
              {m.role === 'assistant' && <div className="ai-msg__av">AI</div>}
               {m.kind === 'answer' && m.answer ? (
                 <div style={{ maxWidth: '100%', width: '100%' }}><AssistantAnswerCard answer={m.answer} scope={frozenScope || freezeAssistantScope(activeClusterId, activeScope)} onCreateInvestigationDraft={handleCreateInvestigationDraft} onCitationClick={(scope) => setFrozenScope(scope)} /></div>
               ) : m.kind === 'suggestion' ? (
                <div style={{ maxWidth: '86%', width: '100%', padding: '12px 14px', borderRadius: 10, fontSize: 13, lineHeight: 1.7,
                  background: 'var(--surface-2)', border: '1px solid var(--warning)', borderLeft: '3px solid var(--warning)',
                  minWidth: 0, overflow: 'hidden' }}>
                  <div style={{ fontWeight: 700, marginBottom: 6 }}>🛠️ 处置建议 · 待确认</div>
                  {/* P2-14: plan 全量展示（不再截断）；C1: 经 react-markdown 渲染 */}
                  {m.plan && (
                    <div className="ai-msg__md" style={{ marginBottom: 8, color: 'var(--text-muted)', fontSize: 13, lineHeight: 1.7 }}>
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>{m.plan}</ReactMarkdown>
                    </div>
                  )}
                  {/* P3-3: 无命令时不显示空命令块 */}
                  {m.script ? (
                    <div style={{ maxWidth: '100%', overflow: 'auto' }}>
                      <div style={{ fontFamily: 'monospace', background: 'var(--surface-3)', padding: '6px 8px', borderRadius: 6,
                        whiteSpace: 'pre-wrap', wordBreak: 'break-all', overflow: 'auto', maxHeight: 220,
                        maxWidth: '100%', boxSizing: 'border-box' }}>{m.script}</div>
                    </div>
                  ) : (
                    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>未生成可执行命令，可在下方输入自定义命令，或让 AI 补充命令。</div>
                  )}
                  {/* P3-4/P1-5: 风险展示 —— 兼容 risk_score 0~1 星级 与 旧版 0~100/文本 */}
                  {(() => { const rv = riskView(m); return rv ? (
                    <div style={{ fontSize: 12, color: rv.color, marginBottom: 8 }}>{rv.text}</div>
                  ) : null })()}
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10 }}>
                    <Button size="small" type="primary" onClick={() => {
                      const query = new URLSearchParams({ source: 'chat', clusterId: activeClusterId, targetType: activeScope.resource?.type || 'cluster', symptom: symptomMsg?.content || '' })
                      if (activeScope.resource?.uid) query.set('resource', activeScope.resource.uid)
                      navigate(`/investigation/new?${query.toString()}`)
                    }}>转为调查草稿</Button>
                    <span style={{ fontSize: 12, color: 'var(--text-muted)', alignSelf: 'center' }}>对话只提供建议，不直接执行动作。</span>
                  </div>
                </div>
              ) : (
                <div style={{ maxWidth: '82%', padding: '10px 14px', borderRadius: 10, fontSize: 13, lineHeight: 1.7,
                  background: m.role === 'user' ? 'var(--primary-soft)' : 'var(--surface-2)', border: m.role === 'user' ? 'none' : '1px solid var(--border)' }}>
                  {/* C1: 助手消息经 react-markdown 渲染；用户消息保持纯文本 pre-wrap */}
                  {m.role === 'assistant' ? (
                    <div className="ai-msg__md">
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>
                        {m.content.replace(/^__investigation_required__\n/, '')}
                      </ReactMarkdown>
                    </div>
                  ) : (
                    <span style={{ whiteSpace: 'pre-wrap' }}>{m.content}</span>
                  )}
                  {/* B2-03/F-07：Chat 识别到结构化调查意图（investigation_required CTA）时，
                      提供显式 createRun 入口（跳转智能调查页发起 Run），实时事实查询统一进入
                      Investigation Tool/Evidence 主链，而非在 Chat 内固定实时采集。 */}
                  {m.role === 'assistant' && m.content.indexOf('__investigation_required__') === 0 && (
                    <div style={{ marginTop: 10 }}>
                      <Button type="primary" size="small"
                        icon={<ExperimentOutlined />}
                        onClick={() => {
                          const query = new URLSearchParams({ source: 'chat', clusterId: activeClusterId, targetType: activeScope.resource?.type || 'cluster', symptom: symptomMsg?.content || '' })
                          if (activeScope.resource?.domain === 'kubernetes' && activeScope.resource.namespace) query.set('namespace', activeScope.resource.namespace)
                          if (activeScope.resource?.uid) {
                            query.set('resource', activeScope.resource.uid)
                            query.set('resourceType', activeScope.resource.type)
                            query.set('resourceName', activeScope.resource.name)
                          }
                          navigate(`/investigation/new?${query.toString()}`)
                        }}>
                        转为正式调查
                      </Button>
                    </div>
                  )}
                  {/* 需求：回复完成（done）且为最新一条助手消息时，可将本次分析加入知识库 */}
                  {m.role === 'assistant' && !loading && i === messages.length - 1
                    && !m.content.startsWith('❌') && !m.content.startsWith('⚠️') && !m.content.startsWith('⏱️')
                    && (m.kind === 'text' || m.kind === 'report') && (
                    <div style={{ marginTop: 8 }}>
                      <Button size="small" type="dashed" icon={<BookOutlined />} loading={!!caseAdding[m.id]}
                        disabled={caseAddedRef.current.has(m.id)}
                        onClick={() => handleAddCase(m, symptomMsg?.content || '')}>
                        {caseAddedRef.current.has(m.id) ? '已加入知识库' : '加入知识库'}
                      </Button>
                    </div>
                  )}
                </div>
              )}
            </div>
            )
          })}
          {progress && <div style={{ fontSize: 12, color: 'var(--text-muted)', padding: '4px 0' }}>{progress}</div>}
          {retryableTurn && <Button size="small" onClick={() => void handleSend(retryableTurn.text, retryableTurn.execResult, true)}>使用相同 turn_id 重试</Button>}
          <div ref={bottomRef} />
        </div>
        <div className="ai-dock__input" style={{ borderTop: '1px solid var(--border-soft)' }}>
          <Input value={input} onChange={(e) => setInput(e.target.value)}
            onPressEnter={() => handleSend()}
            disabled={!hasConcreteCluster}
            placeholder={hasConcreteCluster ? '描述问题，例如：分析 order-svc 错误率突增的根因…' : '请先在顶部选择具体集群'}>
          </Input>
          <Button type="primary" loading={loading} disabled={!hasConcreteCluster}
            icon={<AppIcon name="send" />} onClick={() => handleSend()} style={{ height: 36 }}>发送</Button>
        </div>
      </div>

      <AssistantContextPanel scope={frozenScope || freezeAssistantScope(activeClusterId || activeScope.clusterId, activeScope)} />

      <Drawer title="会话" open={mobileSessionOpen} onClose={() => setMobileSessionOpen(false)}>
        <div style={{ display: 'grid', gap: 4 }}>
          <Button type="primary" onClick={() => { newSession(); setMobileSessionOpen(false) }}>新对话</Button>
          {sessions.map((s) => <Button key={s.session_id} type={activeSession === s.session_id ? 'primary' : 'text'} onClick={() => { void loadSession(s.session_id); setMobileSessionOpen(false) }}>{fmtTitle(s)}</Button>)}
        </div>
      </Drawer>
      <Drawer title="会话 Scope" open={mobileContextOpen} onClose={() => setMobileContextOpen(false)}>
        <AssistantContextPanel scope={frozenScope || freezeAssistantScope(activeClusterId || activeScope.clusterId, activeScope)} />
      </Drawer>

    </div>
  )
}

export default AiChat
