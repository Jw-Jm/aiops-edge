import React, { useEffect, useState, useRef } from 'react'
import { Button, Input, Empty } from 'antd'
import api, { TENANT_ID, getSession, executeSuggestion } from '../../api/client'
import { useSearchParams } from 'react-router-dom'
import AppIcon from '../../components/AppIcons'

interface ChatMessage {
  id: string; role: 'user' | 'assistant'; content: string; timestamp: string
  kind?: 'text' | 'suggestion' | 'execresult'
  plan?: string; script?: string; threadId?: string; riskScore?: number; riskReason?: string
}

const AiChat: React.FC = () => {
  const [searchParams] = useSearchParams()
  const [sessions, setSessions] = useState<any[]>([])
  const [activeSession, setActiveSession] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState(searchParams.get('q') || '')
  const [loading, setLoading] = useState(false)
  const [progress, setProgress] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  const loadSessions = async () => {
    try { const r = await api.get('/ai/sessions'); setSessions(r.data?.sessions || []) } catch {}
  }
  // Issue4: 清除单个会话（checkpoints + session_store）
  const clearOne = async (e: React.MouseEvent, sid: string) => {
    e.stopPropagation()
    if (!window.confirm('确认删除该会话？')) return
    try { await api.delete(`/ai/session/${sid}`) } catch {}
    setSessions((p) => p.filter((s) => s.session_id !== sid))
    if (activeSession === sid) { setActiveSession(''); setMessages([]) }
  }
  // Issue4: 清除全部会话
  const clearAll = async () => {
    if (!window.confirm(`确认清空全部 ${sessions.length} 个历史会话？`)) return
    try { await api.delete('/ai/sessions') } catch {}
    setSessions([]); setActiveSession(''); setMessages([])
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
      ;(d?.messages || []).forEach((m: any, i: number) => {
        if (m.role === 'user') msgs.push({ id: `s-${sid}-${i}`, role: 'user', content: m.content, timestamp: d.created_at || '' })
        else if (m.role === 'assistant') msgs.push({ id: `s-${sid}-${i}`, role: 'assistant', content: m.content, timestamp: d.created_at || '' })
      })
      setMessages(msgs); setActiveSession(sid)
    } catch {}
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

  const newSession = () => { setMessages([]); setActiveSession(''); setInput('') }

  // 需求2/3: 多轮闭环。execResult 非空表示"上一轮处置命令已确认执行"，本轮基于执行结果继续深入分析
  const handleSend = async (preset?: string, execResult?: string) => {
    const text = (preset ?? input).trim()
    if (!text && !execResult) return
    if (loading) return
    const sessionId = activeSession
    setInput('')
    if (text) setMessages((prev) => [...prev, { id: `u-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }])
    setLoading(true); setProgress('正在分析…')
    const controller = new AbortController()
    abortRef.current = controller
    try {
      // 多集群纳管：AI 默认所有集群（cluster_id=all），当前选中某集群时限定该集群
      let clusterId = 'all'
      try {
        const raw = localStorage.getItem('aiops-ui-v3')
        if (raw) { clusterId = JSON.parse(raw)?.state?.currentClusterId || 'all' }
      } catch { clusterId = 'all' }
      const resp = await fetch(`${api.defaults.baseURL}/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': TENANT_ID, Authorization: (api.defaults.headers.common.Authorization as string) || '' },
        body: JSON.stringify({ intent: 'diagnosis', service: '', message: text, stream: true, session_id: sessionId, cluster_id: clusterId, exec_result: execResult || '' }),
        signal: controller.signal,
      })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const reader = resp.body?.getReader(); if (!reader) throw new Error('No stream')
      const decoder = new TextDecoder(); let buf = ''; let fullText = ''
      const pendingSuggestions: any[] = []
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
            if (evName === 'progress' && ev.text) setProgress(ev.text)
            else if (evName === 'chunk' && ev.text) fullText += ev.text
            else if (evName === 'assistant') fullText = ev.content ?? ev.text ?? fullText
            else if (evName === 'done' && !fullText) fullText = ev.text ?? ev.assistant_message?.content ?? ''
            else if (evName === 'error') fullText = `⚠️ ${ev.error ?? ev.text ?? ''}`
            else if (evName === 'suggestion') {
              // Issue1: 每次分析只渲染一张处置建议确认卡。后端每次分析只发一个 suggestion
              // 事件（已去重），此处仅收集 suggestion 类型，避免把 approval_pending 也当一张卡
              // 导致"多个内容一致的处置建议·待确认"。按 threadId 去重（同轮只一张）。
              const tid = ev.thread_id ?? ev.task_id ?? sessionId
              if (!pendingSuggestions.some((s: any) => s.threadId === tid)) {
                pendingSuggestions.push({
                  plan: ev.plan ?? ev.text ?? '', script: ev.script ?? '', threadId: tid,
                  riskScore: ev.risk_score ?? 0, riskReason: ev.risk_reason ?? '',
                })
              }
            }
          } catch {}
        }
      }
      const aiText = fullText || 'LLM 分析未返回结果，请检查配置后重试。'
      const newMsgs: ChatMessage[] = [{ id: `a-${Date.now()}`, role: 'assistant', content: aiText, timestamp: new Date().toISOString() }]
      pendingSuggestions.forEach((s, idx) => {
        newMsgs.push({ id: `sugg-${Date.now()}-${idx}`, role: 'assistant', content: '', kind: 'suggestion',
          plan: s.plan, script: s.script, threadId: s.threadId, riskScore: s.riskScore, riskReason: s.riskReason, timestamp: new Date().toISOString() })
      })
      setMessages((prev) => [...prev, ...newMsgs])
      if (fullText || pendingSuggestions.length) { setActiveSession(sessionId); loadSessions() }
    } catch (err: any) {
      const msg = err?.name === 'AbortError' ? '⏱️ 已中断 / 超时 (120s)' : `❌ 请求失败：${err?.message || ''}`
      setMessages((prev) => [...prev, { id: `e-${Date.now()}`, role: 'assistant', content: msg, timestamp: new Date().toISOString() }])
    } finally {
      setLoading(false); setProgress(''); abortRef.current = null
    }
  }

  // 需求2/3: 确认/自定义命令执行 → 基于执行结果自动发起下一轮深入分析
  const handleExecute = async (m: ChatMessage, customScript?: string) => {
    if (loading) return
    const script = (customScript ?? m.script ?? '').trim()
    if (!script) return
    const isCustom = !!customScript
    // 显示执行中状态
    setMessages((prev) => prev.map((x) => x.id === m.id ? { ...x, content: isCustom ? `⚙️ 执行自定义命令：\n${script}\n\n执行中…` : `⚙️ 确认执行建议命令：\n${script}\n\n执行中…` } : x))
    try {
      const r = await executeSuggestion({ thread_id: m.threadId, script, service: '', context: m.plan || '', approved: true })
      const execResult = r.data?.exec_result || `命令已执行（无输出）\n${script}`
      setMessages((prev) => prev.map((x) => x.id === m.id ? { ...x, kind: 'execresult', content: `✅ 已执行命令：\n${script}\n\n执行结果：\n${execResult}` } : x))
      // 自动发起下一轮深入分析（带执行结果作为上下文）
      await handleSend(undefined, `${script}\n---执行结果---\n${execResult}`)
    } catch (err: any) {
      // P0-2: 失败也要把 kind 改为 execresult，否则 suggestion 卡片忽略 content，用户看不到错误
      const detail = err?.response?.data?.error || err?.message || '未知错误'
      setMessages((prev) => prev.map((x) => x.id === m.id ? { ...x, kind: 'execresult', content: `❌ 执行失败：${detail}\n命令未执行。` } : x))
    }
  }

  // 需求2/3: 驳回处置建议（不执行，仅记录）
  const handleReject = (m: ChatMessage) => {
    setMessages((prev) => prev.map((x) => x.id === m.id ? { ...x, kind: 'execresult', content: '⛔ 已驳回该处置建议，未执行。' } : x))
  }

  return (
    <div style={{ display: 'flex', gap: 16, height: 'calc(100vh - 116px)' }}>
      {/* 会话列表 */}
      <div className="card" style={{ width: 240, flexShrink: 0, marginBottom: 0, display: 'flex', flexDirection: 'column' }}>
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
              <div style={{ fontSize: 12, paddingRight: 16 }}>{s.preview || s.session_id?.slice(0, 20)}</div>
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
      <div className="card" style={{ flex: 1, marginBottom: 0, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <div className="card__head"><span className="card__title">AI 运维助手</span></div>
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
          {messages.map((m) => (
            <div key={m.id} style={{ display: 'flex', gap: 10, marginBottom: 14, justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
              {m.role === 'assistant' && <div className="ai-msg__av">AI</div>}
              {m.kind === 'suggestion' ? (
                <div style={{ maxWidth: '86%', width: '100%', padding: '12px 14px', borderRadius: 10, fontSize: 13, lineHeight: 1.7,
                  background: 'var(--surface-2)', border: '1px solid var(--warning)', borderLeft: '3px solid var(--warning)' }}>
                  <div style={{ fontWeight: 700, marginBottom: 6 }}>🛠️ 处置建议 · 待确认</div>
                  {/* P2-14: plan 只展示方案要点（前 220 字），避免与上方完整分析报告重复 */}
                  {m.plan && <div style={{ whiteSpace: 'pre-wrap', marginBottom: 8, color: 'var(--text-muted)' }}>{(m.plan.length > 220 ? m.plan.slice(0, 220) + '…' : m.plan)}</div>}
                  {/* P3-3: 无命令时不显示空命令块 */}
                  {m.script ? (
                    <div style={{ fontFamily: 'monospace', background: 'var(--surface-3)', padding: '6px 8px', borderRadius: 6, marginBottom: 6, whiteSpace: 'pre-wrap', wordBreak: 'break-all', overflow: 'auto', maxHeight: 200 }}>{m.script}</div>
                  ) : (
                    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>未生成可执行命令，可在下方输入自定义命令，或让 AI 补充命令。</div>
                  )}
                  {/* P3-4: 始终展示风险评分（含 0 分），0 分显示"低" */}
                  <div style={{ fontSize: 12, color: m.riskScore && m.riskScore > 60 ? 'var(--danger)' : m.riskScore && m.riskScore > 30 ? 'var(--warning)' : 'var(--success)', marginBottom: 8 }}>
                    {`风险评分: ${m.riskScore ?? 0}/100${m.riskReason ? `（${m.riskReason}）` : ''}`}
                  </div>
                  <ConfirmCard m={m} onExecute={handleExecute} onReject={handleReject} />
                </div>
              ) : (
                <div style={{ maxWidth: '82%', padding: '10px 14px', borderRadius: 10, whiteSpace: 'pre-wrap', fontSize: 13, lineHeight: 1.7,
                  background: m.role === 'user' ? 'var(--primary-soft)' : 'var(--surface-2)', border: m.role === 'user' ? 'none' : '1px solid var(--border)' }}>
                  {m.content}
                </div>
              )}
            </div>
          ))}
          {progress && <div style={{ fontSize: 12, color: 'var(--text-muted)', padding: '4px 0' }}>{progress}</div>}
          <div ref={bottomRef} />
        </div>
        <div className="ai-dock__input" style={{ borderTop: '1px solid var(--border-soft)' }}>
          <Input value={input} onChange={(e) => setInput(e.target.value)} onPressEnter={() => handleSend()}
            placeholder="描述问题，例如：分析 order-svc 错误率突增的根因…" />
          <Button type="primary" loading={loading} icon={<AppIcon name="send" />} onClick={() => handleSend()} style={{ height: 36 }}>发送</Button>
        </div>
      </div>
    </div>
  )
}

// 需求2/3: 处置建议确认卡——确认执行 / 驳回 / 用户自定义命令执行
const ConfirmCard: React.FC<{ m: ChatMessage; onExecute: (m: ChatMessage, script?: string) => void; onReject: (m: ChatMessage) => void }> = ({ m, onExecute, onReject }) => {
  const [custom, setCustom] = useState('')
  return (
    <div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 6 }}>
        {/* P3-3: 无命令时不显示"确认执行"，仅保留自定义命令与驳回 */}
        {m.script && <Button size="small" type="primary" onClick={() => onExecute(m)}>确认执行</Button>}
        <Button size="small" onClick={() => onReject(m)}>驳回</Button>
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        <Input size="small" placeholder="输入自定义命令后点击执行…" value={custom} onChange={(e) => setCustom(e.target.value)} />
        <Button size="small" disabled={!custom.trim()} onClick={() => onExecute(m, custom)}>执行自定义命令</Button>
      </div>
    </div>
  )
}

export default AiChat
