import { useEffect, useState } from 'react'
import { useUIStore } from '../store/uiStore'
import { listAgents } from '../api/client'

const AgentSidePanel: React.FC = () => {
  const setActive = useUIStore((s) => s.setActiveCommand)
  const [open, setOpen] = useState(false)
  const [agents, setAgents] = useState<any[]>([])

  useEffect(() => {
    listAgents()
      .then((r) => setAgents(r?.data?.agents || []))
      .catch(() => {})
  }, [])

  // Escape 或点击遮罩关闭面板
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

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
        <>
          {/* 半透明遮罩，点击外部关闭 */}
          <div
            onClick={() => setOpen(false)}
            style={{
              position: 'fixed', inset: 0, zIndex: 940,
              background: 'rgba(0,0,0,0.35)',
            }}
          />
          <div
            style={{
              position: 'fixed', right: 0, top: 0, bottom: 0, width: 280, zIndex: 950,
              background: 'var(--surface)', borderLeft: '1px solid var(--border)', padding: 16,
              display: 'flex', flexDirection: 'column',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
              <div style={{ color: 'var(--text)', fontWeight: 700, fontSize: 15 }}>AI 助理</div>
              <button
                onClick={() => setOpen(false)}
                aria-label="关闭"
                style={{
                  background: 'transparent', border: 'none', color: 'var(--text-muted)',
                  cursor: 'pointer', fontSize: 18, lineHeight: 1,
                }}
              >
                ✕
              </button>
            </div>
            <div style={{ flex: 1, overflow: 'auto' }}>
              {agents.map((a) => (
                <div
                  key={a.name}
                  style={{ padding: '10px 12px', borderRadius: 10, border: '1px solid var(--border)', marginBottom: 8, cursor: 'pointer' }}
                  onClick={() => { setActive(a.name); setOpen(false) }}
                >
                  <div style={{ color: 'var(--text)', fontWeight: 600 }}>{a.name}</div>
                  <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>{a.role}</div>
                  <div style={{ color: 'var(--text-muted)', fontSize: 11, marginTop: 4 }}>{a.goal}</div>
                </div>
              ))}
            </div>
          </div>
        </>
      )}
    </>
  )
}

export default AgentSidePanel
