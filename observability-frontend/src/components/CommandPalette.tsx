import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useUIStore } from '../store/uiStore'
import AppIcon from './AppIcons'

interface Cmd { icon: string; label: string; keywords?: string; path?: string; kbd?: string }

const GROUPS: { title: string; items: Cmd[] }[] = [
  {
    title: '总览',
    items: [
      { icon: 'overview', label: '工作台首页', path: '/overview', kbd: 'G O' },
    ],
  },
  {
    title: '可观测',
    items: [
      { icon: 'topology', label: '服务全景', path: '/observability/service', kbd: 'G S' },
      { icon: 'traces', label: '链路追踪', path: '/observability/trace', kbd: 'G T' },
      { icon: 'logs', label: '日志检索', path: '/observability/log', kbd: 'G L' },
      { icon: 'monitor', label: '监控面板', path: '/monitor', kbd: 'G M' },
      { icon: 'capacity', label: '容量预测', path: '/capacity' },
    ],
  },
  {
    title: '告警',
    items: [
      { icon: 'alerts', label: '告警事件', path: '/alerts/events', kbd: 'G A' },
      { icon: 'settings', label: '告警规则', path: '/alerts/rules' },
    ],
  },
  {
    title: '智能运维',
    items: [
      { icon: 'chat', label: 'AI 对话', path: '/ai/chat', kbd: 'G C' },
      { icon: 'tasks', label: '任务工作台', path: '/ai/tasks' },
      { icon: 'workflow', label: '工作流', path: '/ai/workflow' },
      { icon: 'nl2sql', label: 'AI 工具', path: '/ai/tools' },
    ],
  },
  {
    title: '管理与基础设施',
    items: [
      { icon: 'users', label: '用户管理', path: '/admin/users' },
      { icon: 'settings', label: '系统设置', path: '/admin/settings' },
    ],
  },
  {
    title: '报告与合规',
    items: [
      { icon: 'reports', label: '报告中心', path: '/report' },
    ],
  },
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
      } else if (e.key === 'Escape' && open) {
        setOpen(false)
        setQ('')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, setOpen])

  if (!open) return null

  const run = (c: Cmd) => { setOpen(false); setQ(''); if (c.path) navigate(c.path) }

  const filtered = GROUPS.map((g) => ({
    title: g.title,
    items: g.items.filter((c) => !q || c.label.toLowerCase().includes(q.toLowerCase()) || (c.keywords || '').includes(q.toLowerCase())),
  })).filter((g) => g.items.length > 0)

  return (
    <div className="cmdk-backdrop open" onClick={() => setOpen(false)}>
      <div className="cmdk" onClick={(e) => e.stopPropagation()}>
        <input className="cmdk__input" autoFocus value={q} onChange={(e) => setQ(e.target.value)}
          placeholder="搜索页面…  (Esc 关闭)" />
        <div className="cmdk__list">
          {filtered.map((g) => (
            <div key={g.title}>
              <div className="cmdk__group">{g.title}</div>
              {g.items.map((c) => (
                <div key={g.title + c.label} className="cmdk__item" onClick={() => run(c)}>
                  <AppIcon name={c.icon} />
                  <span>{c.label}</span>
                  {c.kbd && <span className="kbd">{c.kbd}</span>}
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

export default CommandPalette
