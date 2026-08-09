import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useUIStore } from '../store/uiStore'

const COMMANDS = [
  // 总览
  { label: '平台总览', path: '/', keywords: 'overview home dashboard 总览' },
  // 可观测
  { label: '服务列表', path: '/services', keywords: 'service 服务' },
  { label: '服务拓扑', path: '/topology', keywords: 'topology graph 拓扑' },
  { label: '拓扑目录', path: '/topology/catalog', keywords: 'catalog 目录' },
  { label: '链路追踪', path: '/traces', keywords: 'trace traceid 链路' },
  { label: '日志查询', path: '/logs', keywords: 'log 日志 victorialogs' },
  { label: 'DeepFlow', path: '/deepflow', keywords: 'deepflow 网络' },
  // 监控
  { label: '监控面板', path: '/monitor', keywords: 'monitor panel 监控 dashboard' },
  { label: '容量预测', path: '/capacity', keywords: 'capacity forecast 容量 预测' },
  // 智能运维
  { label: 'AI 诊断', path: '/aichat', keywords: 'ai chat assistant 诊断' },
  { label: '技能目录', path: '/skills', keywords: 'skill 技能' },
  { label: 'AI 助理', path: '/agents', keywords: 'agent 助理' },
  { label: '工作流', path: '/workflows', keywords: 'workflow 工作流 flow' },
  { label: '告警中心', path: '/alerts', keywords: 'alert 告警 incidents' },
  { label: 'SLO 管理', path: '/slo', keywords: 'slo 服务等级' },
  { label: '审批中心', path: '/approvals', keywords: 'approval 审批' },
  { label: '审计日志', path: '/audit', keywords: 'audit 审计' },
  { label: 'SQL 查询', path: '/nl2sql', keywords: 'sql nl2sql 查询 clickhouse' },
  { label: 'MCP 工具', path: '/mcp', keywords: 'mcp tool 工具' },
  // 管理
  { label: '管理门户', path: '/admin', keywords: 'admin 管理' },
  // 任务
  { label: '任务工作台', path: '/tasks', keywords: 'task 任务' },
  // 智能资产
  { label: '知识库', path: '/knowledge', keywords: 'knowledge 知识 rag' },
  { label: '规则管理', path: '/rules', keywords: 'rule 规则' },
  // 基础设施
  { label: '服务目录', path: '/catalog', keywords: 'catalog 目录' },
  { label: '设备管理', path: '/devices', keywords: 'device 设备' },
  { label: '集群管理', path: '/clusters', keywords: 'cluster 集群' },
  { label: 'K8s 资源', path: '/infrastructure', keywords: 'k8s kubernetes infrastructure 资源' },
  { label: 'SNMP 网络设备', path: '/snmp', keywords: 'snmp 网络设备' },
  { label: '硬件健康', path: '/hardware', keywords: 'hardware 硬件' },
  // 运维工具
  { label: 'WebShell', path: '/shell', keywords: 'shell terminal webshell 终端' },
  { label: '报告中心', path: '/reports', keywords: 'report 报告' },
  { label: '产物中心', path: '/artifacts', keywords: 'artifact 产物' },
  // 设置
  { label: '系统设置', path: '/settings', keywords: 'settings config 设置' },
  { label: '用户管理', path: '/users', keywords: 'user 用户' },
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
