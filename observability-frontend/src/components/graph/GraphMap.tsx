import React, { useEffect, useRef } from 'react'
import { Graph } from '@antv/g6'
import type { GraphSubgraph } from '../../api/graphContracts'

export default function GraphMap({ subgraph, height = 360 }: { subgraph: GraphSubgraph; height?: number }) {
  const container = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!container.current) return
    const graph = new Graph({
      container: container.current,
      autoFit: 'view',
      animation: false,
      layout: { type: 'force', preventOverlap: true },
      node: { style: { size: 28, labelText: (d: any) => d.data?.name || d.id } },
      edge: { style: { endArrow: true, labelText: (d: any) => d.data?.relation_type || '' } },
      data: {
        nodes: subgraph.vertices.map((v) => ({ id: v.entity_uid, data: { name: v.name, entity_type: v.entity_type } })),
        edges: subgraph.edges.map((e) => ({ id: e.edge_uid, source: e.source_uid, target: e.target_uid, data: { relation_type: e.relation_type } })),
      },
    })
    void graph.render()
    return () => graph.destroy()
  }, [subgraph])
  return <div ref={container} data-testid="graph-map" style={{ height, width: '100%', border: '1px solid var(--border-soft)', borderRadius: 8 }} />
}
