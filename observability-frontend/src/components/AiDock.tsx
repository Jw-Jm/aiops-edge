import React, { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useUIStore } from '../store/uiStore'
import AppIcon from './AppIcons'

const AiDock: React.FC = () => {
  const navigate = useNavigate()
  const open = useUIStore((s) => s.aiDockOpen)
  const setOpen = useUIStore((s) => s.setAiDockOpen)

  // Escape 关闭 AI 浮窗
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && open) setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, setOpen])

  const ask = (preset?: string) => {
    const q = (preset || '').trim()
    // P3-2 修复: 空输入直接进对话页（不带 q）；有内容则带 ?q= 让对话页自动发送
    navigate(`/ai/chat${q ? `?q=${encodeURIComponent(q)}` : ''}`)
    setOpen(false)
  }

  return (
    <div className="ai-dock">
      {open && (
        <div className="ai-dock__panel">
          <div className="ai-dock__head">
            <span className="dh-ic"><AppIcon name="chat" /></span>
            <div>
              <div className="dh-title">AI 运维助手</div>
              <div className="dh-sub">自然语言 · 根因分析 · 巡检</div>
            </div>
            <span className="dh-close" onClick={() => setOpen(false)}><AppIcon name="x" /></span>
          </div>
          <div className="ai-dock__prompts">
            <span className="ai-pchip" onClick={() => ask('分析 prod 集群故障根因')}><AppIcon name="sparkles" />分析集群根因</span>
            <span className="ai-pchip" onClick={() => ask('巡检所有 K8s 集群')}><AppIcon name="sparkles" />集群巡检</span>
            <span className="ai-pchip" onClick={() => ask('为什么 order-svc 延迟升高')}>服务延迟排查</span>
          </div>
          <div className="ai-msgs">
            <div className="ai-msg ai-msg--bot">
              <div className="ai-msg__av">AI</div>
              <div className="ai-bubble">你好，我是你的运维助手。用<b>自然语言</b>即可完成故障根因分析、跨集群巡检、日志解读。</div>
            </div>
          </div>
          <div className="ai-dock__input">
            <input className="in" placeholder="问点什么…"
              onKeyDown={(e) => { if (e.key === 'Enter') { const v = (e.target as HTMLInputElement).value; ask(v) } }} />
            <button className="ai-dock__send" title="发送" onClick={() => {
              const inp = document.querySelector('.ai-dock__input input') as HTMLInputElement | null
              ask(inp?.value || '')
            }}><AppIcon name="send" /></button>
          </div>
        </div>
      )}
      <button className="ai-dock__btn" title="AI 运维助手" onClick={() => setOpen(!open)} aria-label="AI 运维助手">
        <AppIcon name="chat" />
      </button>
    </div>
  )
}

export default AiDock
