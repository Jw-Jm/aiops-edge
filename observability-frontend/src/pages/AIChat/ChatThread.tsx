import React, { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Card, Input, Button, Spin, message, Tag, Switch } from 'antd'
import { SendOutlined } from '@ant-design/icons'
import { getSession, approveTask, rejectTask, TENANT_ID } from '../../api/client'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp?: string
}
interface ToolCard {
  tool_call_id: string
  name: string
  status: string
  result?: string
  agent_type?: string   // coordinator | subagent | reviewer | tool
  tool_trace?: { tool: string; result: string }[]
}

const ChatThread: React.FC = () => {
  const { sessionId } = useParams()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [progressText, setProgressText] = useState('')
  const [toolCards, setToolCards] = useState<ToolCard[]>([])
  const [approval, setApproval] = useState<{ task_id: string; plan: string; script: string; risk_score: number; risk_reason: string } | null>(null)
  const [historyLoading, setHistoryLoading] = useState(true)
  const [dualAgent, setDualAgent] = useState(false)   // 批3: 双层 Agent 开关

  // 进入路由回放历史
  useEffect(() => {
    const load = async () => {
      if (!sessionId) {
        setHistoryLoading(false)
        return
      }
      try {
        const r = await getSession(sessionId)
        const msgs = (r?.data?.messages || []).map((m: any) => ({
          id: m.id || `${m.role}-${m.timestamp || Date.now()}`,
          role: m.role === 'user' ? 'user' : 'assistant',
          content: m.content || '',
          timestamp: m.timestamp,
        }))
        setMessages(msgs)
      } catch {
        message.error('加载会话失败')
      } finally {
        setHistoryLoading(false)
      }
    }
    load()
  }, [sessionId])

  const handleSend = async () => {
    const text = input.trim()
    if (!text || loading) return
    setInput('')
    setLoading(true)
    setProgressText('分析开始...')
    setToolCards([])
    setApproval(null)
    const userMsg: ChatMessage = { id: `u-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }
    setMessages((p) => [...p, userMsg])
    try {
      const resp = await fetch('/api/v1/ai/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Tenant-ID': TENANT_ID,
          Authorization: `Bearer ${localStorage.getItem('token') || ''}`,
        },
        body: JSON.stringify({ message: text, stream: true, session_id: sessionId, dual_agent: dualAgent }),
      })
      const reader = resp.body?.getReader()
      if (!reader) return
      const decoder = new TextDecoder()
      let buf = ''
      let fullText = ''
      let toolLocal: ToolCard[] = []
      const dispatch = (evName: string, ev: any) => {
        switch (evName) {
          case 'progress': if (ev.text) setProgressText(ev.text); break
          case 'chunk': if (ev.text) fullText += ev.text; break
          case 'assistant': fullText = ev.content ?? ev.text ?? fullText; break
          case 'tool_start': toolLocal.push({ tool_call_id: ev.tool_call_id, name: ev.name, status: 'pending', agent_type: ev.agent_type, tool_trace: ev.tool_trace }); break
          case 'tool_end':
            toolLocal = toolLocal.map((t) => (t.tool_call_id === ev.tool_call_id ? { ...t, status: ev.status, result: ev.result, agent_type: ev.agent_type ?? t.agent_type, tool_trace: ev.tool_trace ?? t.tool_trace } : t))
            break
          case 'approval_pending':
            setApproval({ task_id: ev.task_id, plan: ev.plan || '', script: ev.script || '', risk_score: ev.risk_score || 0, risk_reason: ev.risk_reason || '' })
            break
          case 'done': if (!fullText) fullText = ev.text ?? ev.assistant_message?.content ?? ''; break
          case 'error': fullText = `⚠️ ${ev.error ?? ev.text ?? ''}`; break
          default: break
        }
      }
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const frames = buf.split('\n\n')
        buf = frames.pop() || ''
        for (const frame of frames) {
          if (!frame.trim()) continue
          let evName = 'message'
          const dataLines: string[] = []
          for (const l of frame.split('\n')) {
            if (l.startsWith('event: ')) evName = l.slice(7).trim()
            else if (l.startsWith('data: ')) dataLines.push(l.slice(6))
          }
          if (dataLines.length === 0) continue
          try {
            dispatch(evName, JSON.parse(dataLines.join('\n')))
          } catch {}
        }
      }
      setToolCards(toolLocal)
      if (fullText) {
        setMessages((p) => [...p, { id: `a-${Date.now()}`, role: 'assistant', content: fullText, timestamp: new Date().toISOString() }])
      }
    } catch {
      message.error('发送失败')
    } finally {
      setLoading(false)
      setProgressText('')
    }
  }

  if (historyLoading) return <Spin style={{ display: 'block', margin: '40px auto' }} />
  return (
    <Card
      title={`会话 #${sessionId}`}
      style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10, height: 'calc(100vh - 140px)', display: 'flex', flexDirection: 'column' }}
      styles={{ body: { flex: 1, display: 'flex', flexDirection: 'column' } }}
    >
      <div style={{ flex: 1, overflowY: 'auto', marginBottom: 16 }}>
        {messages.map((m) => (
          <div key={m.id} style={{ display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start', marginBottom: 12 }}>
            <div
              style={{
                maxWidth: '72%',
                padding: '10px 14px',
                borderRadius: 12,
                background: m.role === 'user' ? '#2563eb' : 'var(--surface-2)',
                color: m.role === 'user' ? '#fff' : 'var(--text)',
                whiteSpace: 'pre-wrap',
              }}
            >
              {m.content}
            </div>
          </div>
        ))}
        {toolCards.map((t) => {
          // agent_type / status 英文 → 中文，降低理解门槛
          const agentText = { coordinator: '主控', subagent: '子 Agent', reviewer: '审阅', tool: '工具' }[t.agent_type || 'tool'] || t.agent_type || '工具'
          const tagColor = t.agent_type === 'coordinator' ? 'purple' : t.agent_type === 'reviewer' ? 'orange' : t.agent_type === 'subagent' ? 'blue' : 'default'
          const statusText = t.status === 'success' ? '成功' : t.status === 'pending' ? '执行中' : t.status === 'error' ? '失败' : t.status || ''
          return (
            <div key={t.tool_call_id} style={{ display: 'flex', flexDirection: 'column', gap: 4, padding: '6px 12px', marginBottom: 6, background: 'var(--surface-2)', borderRadius: 8 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Tag color={tagColor}>{agentText}</Tag>
                <span style={{ fontSize: 12, color: 'var(--text)' }}>⚙️ {t.name}</span>
                <span style={{ fontSize: 11, color: t.status === 'success' ? '#22c55e' : '#a1a1aa' }}>{statusText}</span>
              </div>
              {t.agent_type === 'subagent' && t.tool_trace && t.tool_trace.length > 0 && (
                <div style={{ marginLeft: 24, fontSize: 11, color: 'var(--text-muted)' }}>
                  {t.tool_trace.map((tr, i) => <div key={i}>→ {tr.tool}: {tr.result.slice(0, 60)}</div>)}
                </div>
              )}
            </div>
          )
        })}
        {approval && (
          <div style={{ display: 'flex', flexDirection: 'column', marginBottom: 12, padding: '12px 14px', background: 'var(--surface-2)', border: '1px solid #d97706', borderRadius: 8 }}>
            <div style={{ fontSize: 13, color: 'var(--text)', fontWeight: 600, marginBottom: 4 }}>⏳ 待人工审批</div>
            <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 4 }}>
              {approval.plan} · 风险 {Math.round((approval.risk_score || 0) * 100)}% {approval.risk_reason ? `· ${approval.risk_reason}` : ''}
            </div>
            <pre style={{ background: 'var(--surface)', padding: 8, borderRadius: 6, fontSize: 12, color: 'var(--text)', whiteSpace: 'pre-wrap' }}>
              {approval.script}
            </pre>
            <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
              <Button size="small" type="primary" onClick={() => {
                approveTask(approval.task_id).then(() => { message.success('已批准执行'); setApproval(null) }).catch(() => message.error('审批失败'))
              }}>批准执行</Button>
              <Button size="small" danger onClick={() => {
                rejectTask(approval.task_id).then(() => { message.success('已拒绝'); setApproval(null) }).catch(() => message.error('操作失败'))
              }}>拒绝</Button>
            </div>
          </div>
        )}
        {loading && <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>🤖 {progressText}</div>}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <Switch size="small" checked={dualAgent} onChange={setDualAgent} />
        <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>双层 Agent（Coordinator/子Agent/Reviewer）</span>
      </div>
      <div style={{ display: 'flex', gap: 8 }}>
        <Input.TextArea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="输入消息..."
          autoSize={{ minRows: 1, maxRows: 4 }}
          onPressEnter={(e) => {
            if (!e.shiftKey) {
              e.preventDefault()
              handleSend()
            }
          }}
        />
        <Button type="primary" icon={<SendOutlined />} onClick={handleSend} loading={loading}>发送</Button>
      </div>
    </Card>
  )
}

export default ChatThread
