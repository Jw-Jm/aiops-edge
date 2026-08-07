import { useState } from 'react'
import { useUIStore } from '../store/uiStore'

const AGENTS = [
  { id: 'rca', name: 'RCA 根因分析', desc: '定位服务异常根因', status: 'ready' },
  { id: 'holmes', name: 'Holmes 链路调查', desc: '深度追踪调用链', status: 'ready' },
  { id: 'query', name: 'SQL 查询专家', desc: 'NL 转 ClickHouse SQL', status: 'ready' },
  { id: 'ops', name: '运维执行', desc: '安全执行运维操作', status: 'ready' },
]

const AgentSidePanel: React.FC = () => {
  const setActive = useUIStore((s) => s.setActiveCommand)
  const [open, setOpen] = useState(false)
  // 预留：后续从 /agents 接口拉取真实 agent 列表
  return (
    <>
      <button
        onClick={() => setOpen(!open)}
        title="AI 助理"
        style={{
          position: 'fixed', right: 0, top: '50%', transform: 'translateY(-50%)', zIndex: 900,
          background: 'var(--surface)', color: 'var(--text)', border: '1px solid var(--border)',
          borderRight: 'none', borderRadius: '8px 0 0 8px', padding: '10px 6px', cursor: 'pointer',
        }}
      >
        🤖
      </button>
      {open && (
        <div
          style={{
            position: 'fixed', right: 0, top: 0, bottom: 0, width: 280, zIndex: 950,
            background: 'var(--surface)', borderLeft: '1px solid var(--border)', padding: 16,
          }}
        >
          <div style={{ color: 'var(--text)', fontWeight: 700, fontSize: 15, marginBottom: 12 }}>AI 助理</div>
          {AGENTS.map((a) => (
            <div
              key={a.id}
              style={{ padding: '10px 12px', borderRadius: 10, border: '1px solid var(--border)', marginBottom: 8, cursor: 'pointer' }}
              onClick={() => { setActive(a.id); setOpen(false) }}
            >
              <div style={{ color: 'var(--text)', fontWeight: 600 }}>{a.name}</div>
              <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>{a.desc}</div>
              <div style={{ color: '#22c55e', fontSize: 11, marginTop: 4 }}>● {a.status}</div>
            </div>
          ))}
        </div>
      )}
    </>
  )
}

export default AgentSidePanel
