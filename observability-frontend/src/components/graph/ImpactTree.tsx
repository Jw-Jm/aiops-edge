import React from 'react'
import type { GraphSubgraph } from '../../api/graphContracts'

export default function ImpactTree({ subgraph }: { subgraph: GraphSubgraph }) {
  return <section aria-label="影响树"><h3>影响树</h3><ul>{subgraph.vertices.map((vertex) => <li key={vertex.entity_uid}>{vertex.name}（{vertex.entity_type}）</li>)}</ul>{subgraph.meta.partial && <p>已达到服务器返回上限，请缩小范围。</p>}</section>
}
