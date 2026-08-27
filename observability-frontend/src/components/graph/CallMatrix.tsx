import React from 'react'
import type { GraphEdge } from '../../api/graphContracts'

export default function CallMatrix({ edges }: { edges: GraphEdge[] }) {
  return <section aria-label="调用矩阵"><h3>调用矩阵</h3><div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 100px', gap: 4 }}>{edges.map((edge) => <React.Fragment key={edge.edge_uid}><span>{edge.source_uid}</span><span>{edge.target_uid}</span><strong>{edge.relation_type}</strong></React.Fragment>)}</div></section>
}
