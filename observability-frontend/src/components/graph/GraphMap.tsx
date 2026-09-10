import React, { useEffect, useMemo, useRef } from 'react'
import { Graph } from '@antv/g6'
import type { GraphSubgraph } from '../../api/graphContracts'
import { buildGraphDisplayModel, layoutForMode, type GraphLayoutMode, type GraphViewMode } from './graphPresentation'

export default function GraphMap({ subgraph, height = 420, mode = 'expert', layoutOverride, centerUid, maxNodes = 80, maxEdges = 200 }: { subgraph: GraphSubgraph; height?: number; mode?: GraphViewMode; layoutOverride?: GraphLayoutMode; centerUid?: string; maxNodes?: number; maxEdges?: number }) {
  const container = useRef<HTMLDivElement>(null)
  const display = useMemo(() => buildGraphDisplayModel(subgraph, { mode, centerUid: centerUid ?? subgraph.center_entity_uid, maxNodes, maxEdges }), [centerUid, maxEdges, maxNodes, mode, subgraph])
  useEffect(() => {
    if (!container.current) return
    const layout = layoutOverride ?? layoutForMode(mode)
    const graph = new Graph({
      container: container.current,
      autoFit: 'view',
      animation: false,
      layout: layout === 'radial' ? { type: 'radial', unitRadius: 100 } : { type: 'dagre', rankdir: layout === 'hierarchy-tb' ? 'TB' : 'LR', nodesep: 36, ranksep: 90 },
      node: { type: 'rect', style: { size: [150, 52], radius: 8, fill: '#ffffff', stroke: '#dde3ea', lineWidth: (d: any) => d.data?.aggregate ? 1 : 2, labelText: (d: any) => d.data?.label || d.id, labelFill: '#172033', labelMaxLines: 2, labelWordWrap: true } },
      edge: { type: 'polyline', style: { endArrow: true, stroke: (d: any) => d.data?.style === 'failure' ? '#c9362b' : d.data?.style === 'inferred' ? '#a46f0a' : '#6f7d91', lineWidth: 1.5, lineDash: (d: any) => d.data?.style === 'inferred' ? [5, 4] : undefined, labelText: (d: any) => d.data?.label || '', labelFill: '#172033', labelBackgroundFill: '#ffffff', labelBackgroundOpacity: 0.96, labelBackgroundRadius: 4, labelPadding: [2, 4] } },
      data: {
        nodes: display.nodes.map((node) => ({ id: node.id, data: { name: node.name, label: `${node.typeLabel} · ${node.label}`, entity_type: node.entityType, iconKey: node.iconKey, healthTone: node.healthTone, aggregate: node.aggregate } })),
        edges: display.edges.map((edge) => ({ id: edge.id, source: edge.source, target: edge.target, data: { relationType: edge.relationType, label: edge.label, style: edge.style, factStatus: edge.factStatus, sourceRef: edge.sourceRef, syncedAt: edge.syncedAt } })),
      },
    })
    void graph.render()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(() => { void (graph as any).fitView?.() })
    if (observer && container.current) observer.observe(container.current)
    return () => { observer?.disconnect(); graph.destroy() }
  }, [display, height, layoutOverride, mode])
  const omitted = Object.values(display.omittedByType).reduce((sum, count) => sum + count, 0)
  return <div ref={container} data-testid="graph-map" aria-label="资源关系图" style={{ height, width: '100%', border: '1px solid var(--border-soft)', borderRadius: 8, position: 'relative' }}>{omitted > 0 && <span className="graph-map__omitted">画布预算：已聚合 {omitted} 个节点</span>}</div>
}
