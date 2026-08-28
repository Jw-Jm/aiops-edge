import React, { useEffect, useMemo, useRef } from 'react'
import { Graph } from '@antv/g6'
import type { GraphSubgraph } from '../../api/graphContracts'

export default function GraphMap({ subgraph, height = 360 }: { subgraph: GraphSubgraph; height?: number }) {
  const container = useRef<HTMLDivElement>(null)
  const bounded = useMemo(() => {
    const vertices = subgraph.vertices.slice(0, 300)
    const vertexIds = new Set(vertices.map((vertex) => vertex.entity_uid))
    const edges = subgraph.edges
      .filter((edge) => vertexIds.has(edge.source_uid) && vertexIds.has(edge.target_uid))
      .slice(0, 1000)
    return { vertices, edges }
  }, [subgraph])
  useEffect(() => {
    if (!container.current) return
    const graph = new Graph({
      container: container.current,
      autoFit: 'view',
      animation: false,
      layout: { type: 'dagre', rankdir: 'LR', nodesep: 36, ranksep: 90 },
      node: { style: { size: 28, labelText: (d: any) => d.data?.name || d.id } },
      edge: { style: { endArrow: true, labelText: (d: any) => d.data?.relation_type || '' } },
      data: {
        nodes: bounded.vertices.map((v) => ({ id: v.entity_uid, data: { name: v.name, entity_type: v.entity_type } })),
        edges: bounded.edges.map((e) => ({ id: e.edge_uid, source: e.source_uid, target: e.target_uid, data: { relation_type: e.relation_type } })),
      },
    })
    void graph.render()
    return () => graph.destroy()
  }, [bounded])
  return <div ref={container} data-testid="graph-map" aria-label="服务地图" style={{ height, width: '100%', border: '1px solid var(--border-soft)', borderRadius: 8 }} />
}
