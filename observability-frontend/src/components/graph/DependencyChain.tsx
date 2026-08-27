import React from 'react'
import type { GraphEntity, GraphEdge } from '../../api/graphContracts'

export default function DependencyChain({ vertices, edges }: { vertices: GraphEntity[]; edges: GraphEdge[] }) {
  return <section aria-label="依赖主链"><h3 style={{ marginBottom: 8 }}>依赖主链</h3><div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
    {vertices.map((v, index) => <React.Fragment key={v.entity_uid}><span style={{ padding: '5px 9px', borderRadius: 999, background: 'var(--bg-soft)' }}>{v.name} <small>{v.entity_type}</small></span>{index < vertices.length - 1 ? <span aria-hidden="true">→</span> : null}</React.Fragment>)}
  </div><small style={{ color: 'var(--text-muted)' }}>{edges.length} 条关系 · UID 可追溯</small></section>
}
