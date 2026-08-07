import React, { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Card, Input, Button, Spin, message } from 'antd'
import { SendOutlined } from '@ant-design/icons'
import { getSession } from '../../api/client'

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
}

const ChatThread: React.FC = () => {
  const { sessionId } = useParams()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [progressText, setProgressText] = useState('')
  const [toolCards, setToolCards] = useState<ToolCard[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)

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
    const userMsg: ChatMessage = { id: `u-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }
    setMessages((p) => [...p, userMsg])
    try {
      const resp = await fetch('/api/v1/ai/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Tenant-ID': 'default',
          Authorization: `Bearer ${localStorage.getItem('token') || ''}`,
        },
        body: JSON.stringify({ message: text, stream: true, session_id: sessionId }),
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
          case 'tool_start': toolLocal.push({ tool_call_id: ev.tool_call_id, name: ev.name, status: 'pending' }); break
          case 'tool_end':
            toolLocal = toolLocal.map((t) => (t.tool_call_id === ev.tool_call_id ? { ...t, status: ev.status, result: ev.result } : t))
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
        {toolCards.map((t) => (
          <div key={t.tool_call_id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px', marginBottom: 6, background: 'var(--surface-2)', borderRadius: 8 }}>
            <span style={{ fontSize: 12, color: 'var(--text)' }}>⚙️ {t.name}</span>
            <span style={{ fontSize: 11, color: t.status === 'success' ? '#22c55e' : '#a1a1aa' }}>{t.status}</span>
          </div>
        ))}
        {loading && <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>🤖 {progressText}</div>}
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
