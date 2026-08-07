import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useUIStore } from '../store/uiStore'

const COMMANDS = [
  { label: '平台总览', path: '/', keywords: 'overview home dashboard' },
  { label: 'AI 诊断', path: '/aichat', keywords: 'ai chat assistant' },
  { label: '服务列表', path: '/services', keywords: 'service' },
  { label: '服务拓扑', path: '/topology', keywords: 'topology graph' },
  { label: '链路追踪', path: '/traces', keywords: 'trace' },
  { label: '日志查询', path: '/logs', keywords: 'log' },
  { label: '告警中心', path: '/alerts', keywords: 'alert' },
  { label: '监控面板', path: '/monitor', keywords: 'monitor panel' },
  { label: '任务工作台', path: '/tasks', keywords: 'task' },
  { label: '系统设置', path: '/settings', keywords: 'settings config' },
]

const CommandPalette: React.FC = () => {
  const navigate = useNavigate()
  const open = useUIStore((s) => s.commandOpen)
  const setOpen = useUIStore((s) => s.setCommandOpen)
  const [q, setQ] = useState('')

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'p')) {
        e.preventDefault()
        setOpen(!open)
        setQ('')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, setOpen])

  if (!open) return null
  const list = COMMANDS.filter(
    (c) =>
      !q ||
      c.label.toLowerCase().includes(q.toLowerCase()) ||
      c.keywords.includes(q.toLowerCase()),
  )
  return (
    <div
      onClick={() => setOpen(false)}
      style={{
        position: 'fixed', inset: 0, zIndex: 1000, background: 'rgba(0,0,0,0.6)',
        display: 'flex', alignItems: 'flex-start', justifyContent: 'center', paddingTop: '18vh',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 480, background: 'var(--surface)', border: '1px solid var(--border)',
          borderRadius: 12, padding: 12, boxShadow: '0 16px 48px rgba(0,0,0,0.4)',
        }}
      >
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="输入命令或搜索页面…"
          style={{
            width: '100%', background: 'transparent', border: 'none', outline: 'none',
            color: 'var(--text)', fontSize: 15, padding: '8px 4px',
          }}
        />
        <div style={{ marginTop: 8 }}>
          {list.map((c) => (
            <div
              key={c.path}
              onClick={() => { setOpen(false); navigate(c.path) }}
              style={{ padding: '8px 10px', borderRadius: 8, cursor: 'pointer', color: 'var(--text)' }}
              onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--surface-2)')}
              onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
            >
              {c.label}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

export default CommandPalette
