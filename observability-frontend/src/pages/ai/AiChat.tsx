import React, { useEffect, useState, useRef } from 'react'
import { Button, Input, Empty } from 'antd'
import api, { TENANT_ID, getSession } from '../../api/client'
import { useSearchParams } from 'react-router-dom'
import AppIcon from '../../components/AppIcons'

interface ChatMessage { id: string; role: 'user' | 'assistant'; content: string; timestamp: string }

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

  const handleSend = async (preset?: string) => {
    const text = (preset ?? input).trim()
    if (!text || loading) return
    const sessionId = activeSession
    setInput('')
    setMessages((prev) => [...prev, { id: `u-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }])
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
        body: JSON.stringify({ intent: 'diagnosis', service: '', message: text, stream: true, session_id: sessionId, cluster_id: clusterId }),
        signal: controller.signal,
      })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const reader = resp.body?.getReader(); if (!reader) throw new Error('No stream')
      const decoder = new TextDecoder(); let buf = ''; let fullText = ''
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
          } catch {}
        }
      }
      const aiText = fullText || 'LLM 分析未返回结果，请检查配置后重试。'
      setMessages((prev) => [...prev, { id: `a-${Date.now()}`, role: 'assistant', content: aiText, timestamp: new Date().toISOString() }])
      if (fullText) { setActiveSession(sessionId); loadSessions() }
    } catch (err: any) {
      const msg = err?.name === 'AbortError' ? '⏱️ 已中断 / 超时 (120s)' : `❌ 请求失败：${err?.message || ''}`
      setMessages((prev) => [...prev, { id: `e-${Date.now()}`, role: 'assistant', content: msg, timestamp: new Date().toISOString() }])
    } finally {
      setLoading(false); setProgress(''); abortRef.current = null
    }
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
              <div style={{ maxWidth: '82%', padding: '10px 14px', borderRadius: 10, whiteSpace: 'pre-wrap', fontSize: 13, lineHeight: 1.7,
                background: m.role === 'user' ? 'var(--primary-soft)' : 'var(--surface-2)', border: m.role === 'user' ? 'none' : '1px solid var(--border)' }}>
                {m.content}
              </div>
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

export default AiChat
