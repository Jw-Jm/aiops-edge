import React, { useEffect, useState, useRef } from 'react'
import { Button, Input, Empty, Alert, message } from 'antd'
import { BookOutlined } from '@ant-design/icons'
import api, { TENANT_ID, getSession, executeSuggestion, finalReport, addKnowledgeCase } from '../../api/client'
import { useSearchParams } from 'react-router-dom'
import AppIcon from '../../components/AppIcons'

interface ChatMessage {
  id: string; role: 'user' | 'assistant'; content: string; timestamp: string
  kind?: 'text' | 'suggestion' | 'execresult' | 'report'
  plan?: string; script?: string; threadId?: string; riskScore?: number; riskReason?: string; service?: string
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

const AiChat: React.FC = () => {
  const [searchParams] = useSearchParams()
  const [sessions, setSessions] = useState<any[]>([])
  const [activeSession, setActiveSession] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState(searchParams.get('q') || '')
  const [loading, setLoading] = useState(false)
  const [progress, setProgress] = useState('')
  // P0-1: 后端 SSE notice 事件（type=notice, level=warning, text=...）→ 消息区顶部黄色提示条
  const [notice, setNotice] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  // 需求：对话沉淀故障案例——caseAdding: 防重 loading；caseAdded: 记录已入库的消息，避免重复提交
  const [caseAdding, setCaseAdding] = useState<Record<string, boolean>>({})
  const caseAddedRef = useRef<Set<string>>(new Set())

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
      // 修复：直接从 localStorage 取 token（client.ts 初始化时 localStorage 为空，
      // axios defaults 未写入；登录后只 sync 到 localStorage，未回写 axios defaults，
      // 导致 fetch 读到空 Authorization 头 → 401）。此处直接读 localStorage 兜底。
      const tok = localStorage.getItem('token') || ''
      const resp = await fetch(`${api.defaults.baseURL}/ai/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': TENANT_ID, Authorization: tok ? `Bearer ${tok}` : '' },
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
            else if (evName === 'notice') {
              // P0-1: notice 事件 → 消息区顶部黄色提示条（用户可关闭）
              const text = ev.text ?? ev.message ?? ev.content ?? ''
              if (text) setNotice(String(text))
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

  // 需求2/3: 生成最终版本报告，作为普通消息追加到对话
  const handleFinalReport = async (m: ChatMessage) => {
    if (loading) return
    setLoading(true); setProgress('正在生成最终版本报告…')
    try {
      const r = await finalReport({ session_id: activeSession || m.threadId, service: m.service || '' })
      const report = r.data?.report || '（未生成报告）'
      setMessages((prev) => [...prev, { id: `rep-${Date.now()}`, role: 'assistant', kind: 'report',
        content: report, timestamp: new Date().toISOString() }])
    } catch (err: any) {
      setMessages((prev) => [...prev, { id: `re-${Date.now()}`, role: 'assistant',
        content: `❌ 生成最终报告失败：${err?.message || ''}`, timestamp: new Date().toISOString() }])
    } finally { setLoading(false); setProgress('') }
  }

  // 需求：处置建议确认执行成功后，一键沉淀为故障案例（POST /ai/knowledge/case）
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
        if (d.inserted === false) message.warning('已存在相似案例')
        else message.success(d.case_id ? `已加入知识库 (案例 ${d.case_id})` : '已加入知识库')
      })
      .catch((e: any) => {
        setCaseAdding((p) => ({ ...p, [m.id]: false }))
        const err = e?.response?.data?.error || e?.response?.data?.detail || e?.message || '加入失败'
        message.error(`质量审查未通过：${err}`)
      })
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
          {/* P0-1: SSE notice 事件提示条（黄色警告，可关闭） */}
          {notice && (
            <Alert type="warning" showIcon closable message={notice}
              onClose={() => setNotice('')} style={{ marginBottom: 12 }} />
          )}
          {messages.map((m, i) => {
            // 需求：沉淀故障案例时取当前建议之前最近一条用户问题作为 symptom
            const symptomMsg = [...messages.slice(0, i)].reverse().find((x) => x.role === 'user')
            return (
            <div key={m.id} style={{ display: 'flex', gap: 10, marginBottom: 14, justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
              {m.role === 'assistant' && <div className="ai-msg__av">AI</div>}
              {m.kind === 'suggestion' ? (
                <div style={{ maxWidth: '86%', width: '100%', padding: '12px 14px', borderRadius: 10, fontSize: 13, lineHeight: 1.7,
                  background: 'var(--surface-2)', border: '1px solid var(--warning)', borderLeft: '3px solid var(--warning)',
                  minWidth: 0, overflow: 'hidden' }}>
                  <div style={{ fontWeight: 700, marginBottom: 6 }}>🛠️ 处置建议 · 待确认</div>
                  {/* P2-14: plan 全量展示（不再截断） */}
                  {m.plan && <div style={{ whiteSpace: 'pre-wrap', marginBottom: 8, color: 'var(--text-muted)' }}>{m.plan}</div>}
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
                  <ConfirmCard m={m} symptom={symptomMsg?.content || ''}
                    caseLoading={!!caseAdding[m.id]} caseAdded={caseAddedRef.current.has(m.id)}
                    onExecute={handleExecute} onReject={handleReject} onFinalReport={handleFinalReport} onAddCase={handleAddCase} />
                </div>
              ) : (
                <div style={{ maxWidth: '82%', padding: '10px 14px', borderRadius: 10, whiteSpace: 'pre-wrap', fontSize: 13, lineHeight: 1.7,
                  background: m.role === 'user' ? 'var(--primary-soft)' : 'var(--surface-2)', border: m.role === 'user' ? 'none' : '1px solid var(--border)' }}>
                  {m.content}
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

// 需求2/3: 处置建议确认卡——确认执行 / 驳回 / 用户自定义命令执行 / 执行成功后加入知识库
const ConfirmCard: React.FC<{
  m: ChatMessage
  symptom: string
  caseLoading: boolean
  caseAdded: boolean
  onExecute: (m: ChatMessage, script?: string) => void
  onReject: (m: ChatMessage) => void
  onFinalReport: (m: ChatMessage) => void
  onAddCase: (m: ChatMessage, symptom: string) => void
}> = ({ m, symptom, caseLoading, caseAdded, onExecute, onReject, onFinalReport, onAddCase }) => {
  const [custom, setCustom] = useState('')
  // 需求：确认执行成功（content 以 ✅ 开头且无失败标记）或回复完成时展示「加入知识库」
  const executedOk = m.kind === 'execresult' || m.kind === 'report'
  const succeeded = executedOk && !m.content.includes('❌') && !m.content.includes('失败') && !m.content.includes('⛔')
  return (
    <div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 6, flexWrap: 'wrap' }}>
        {/* P3-3: 无命令时不显示"确认执行"，仅保留自定义命令与驳回 */}
        {m.script && <Button size="small" type="primary" onClick={() => onExecute(m)}>确认执行</Button>}
        <Button size="small" onClick={() => onReject(m)}>驳回</Button>
        <Button size="small" onClick={() => onFinalReport(m)}>输出最终版本报告</Button>
        {succeeded && (
          <Button size="small" type="dashed" icon={<BookOutlined />} loading={caseLoading}
            disabled={caseAdded}
            onClick={() => onAddCase(m, symptom)}>{caseAdded ? '已加入知识库' : '加入知识库'}</Button>
        )}
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        <Input size="small" placeholder="输入自定义命令后点击执行…" value={custom} onChange={(e) => setCustom(e.target.value)} />
        <Button size="small" disabled={!custom.trim()} onClick={() => onExecute(m, custom)}>执行自定义命令</Button>
      </div>
    </div>
  )
}

export default AiChat
