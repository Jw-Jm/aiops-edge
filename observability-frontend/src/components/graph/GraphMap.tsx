import React, { useEffect, useMemo, useRef } from 'react'
import { Graph } from '@antv/g6'
import type { GraphSubgraph } from '../../api/graphContracts'
import { buildGraphDisplayModel, layoutForMode, type GraphLayoutMode, type GraphViewMode } from './graphPresentation'

export interface GraphMapProps {
  subgraph: GraphSubgraph
  height?: number
  mode?: GraphViewMode
  layoutOverride?: GraphLayoutMode
  centerUid?: string
  maxNodes?: number
  maxEdges?: number
  focusedNodeId?: string | null
  focusedEdgeId?: string | null
  expandedContextUids?: ReadonlySet<string>
  onNodeSelect?: (nodeId: string | null) => void
  onEdgeSelect?: (edgeId: string | null) => void
  onCanvasSelect?: () => void
}

/**
 * 资源关系画布。
 *
 * 视觉合同：节点最小 196×64、正文 ≥12px；箭头 ≥10px；关系标签 ≥12px 且不透明底色；
 * 层间距 ≥120px 保证关系名不重叠。所有可见边都直接显示中文关系与方向。
 */
export default function GraphMap({
  subgraph,
  height = 420,
  mode = 'expert',
  layoutOverride,
  centerUid,
  maxNodes = 80,
  maxEdges = 200,
  focusedNodeId = null,
  focusedEdgeId = null,
  expandedContextUids,
  onNodeSelect,
  onEdgeSelect,
  onCanvasSelect,
}: GraphMapProps) {
  const container = useRef<HTMLDivElement>(null)
  const handlers = useRef({ onNodeSelect, onEdgeSelect, onCanvasSelect })
  handlers.current = { onNodeSelect, onEdgeSelect, onCanvasSelect }
  const display = useMemo(
    () => buildGraphDisplayModel(subgraph, { mode, centerUid: centerUid ?? subgraph.center_entity_uid, maxNodes, maxEdges, ...(expandedContextUids ? { expandedContextUids } : {}) }),
    [centerUid, expandedContextUids, maxEdges, maxNodes, mode, subgraph],
  )
  useEffect(() => {
    if (!container.current) return
    const layout = layoutOverride ?? layoutForMode(mode)
    const edgeStroke = (d: any) => d.data?.style === 'failure' ? '#c83c35' : d.data?.style === 'inferred' ? '#a87708' : '#6f7d91'
    const graph = new Graph({
      container: container.current,
      autoFit: 'view',
      animation: false,
      layout: layout === 'radial' ? { type: 'radial', unitRadius: 110 } : { type: 'dagre', rankdir: layout === 'hierarchy-tb' ? 'TB' : 'LR', nodesep: 54, ranksep: 132 },
      node: {
        type: 'rect',
        style: {
          size: [196, 64],
          radius: 8,
          fill: '#ffffff',
          stroke: (d: any) => d.data?.healthTone === 'critical' ? '#c9362b' : d.data?.healthTone === 'degraded' ? '#c26a1b' : d.data?.healthTone === 'healthy' ? '#168654' : '#6b7482',
          lineWidth: (d: any) => d.data?.aggregate ? 1 : 2,
          opacity: (d: any) => d.data?.dimmed ? 0.25 : 1,
          labelText: (d: any) => d.data?.label || d.id,
          labelFill: '#162033',
          labelFontSize: 12,
          labelMaxWidth: 174,
          labelMaxLines: 2,
          labelWordWrap: true,
        },
      },
      edge: {
        type: 'polyline',
        style: {
          endArrow: true,
          endArrowType: 'triangle',
          endArrowSize: 10,
          endArrowFill: edgeStroke,
          endArrowStroke: edgeStroke,
          stroke: edgeStroke,
          lineWidth: (d: any) => d.data?.style === 'failure' ? 2.2 : 1.6,
          lineDash: (d: any) => d.data?.style === 'inferred' ? [5, 4] : undefined,
          opacity: (d: any) => d.data?.dimmed ? 0.2 : 1,
          labelText: (d: any) => d.data?.label || '',
          labelFill: '#162033',
          labelFontSize: 12,
          labelBackgroundFill: '#ffffff',
          labelBackgroundOpacity: 1,
          labelBackgroundRadius: 4,
          labelPadding: [3, 6],
        },
      },
      behaviors: ['drag-canvas', 'zoom-canvas', 'drag-element'],
      data: {
        nodes: display.nodes.map((node) => ({
          id: node.id,
          data: {
            name: node.name,
            label: `${node.typeLabel} · ${node.label}`,
            entity_type: node.entityType,
            iconKey: node.iconKey,
            healthTone: node.healthTone,
            aggregate: node.aggregate,
            dimmed: focusedNodeId ? (node.id !== focusedNodeId && !isNeighbor(display, focusedNodeId, node.id)) : false,
          },
        })),
        edges: display.edges.map((edge) => ({
          id: edge.id,
          source: edge.source,
          target: edge.target,
          data: {
            relationType: edge.relationType,
            label: edge.label,
            style: edge.style,
            semantic: edge.semantic,
            factStatus: edge.factStatus,
            sourceRef: edge.sourceRef,
            syncedAt: edge.syncedAt,
            dimmed: focusedEdgeId ? edge.id !== focusedEdgeId : Boolean(edge.dimmed),
          },
        })),
      },
    })
    graph.on('node:click', (event: any) => handlers.current.onNodeSelect?.(String(event?.target?.id ?? '') || null))
    graph.on('edge:click', (event: any) => handlers.current.onEdgeSelect?.(String(event?.target?.id ?? '') || null))
    graph.on('canvas:click', () => handlers.current.onCanvasSelect?.())
    void graph.render()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(() => { void (graph as any).fitView?.() })
    if (observer && container.current) observer.observe(container.current)
    return () => { observer?.disconnect(); graph.destroy() }
  }, [display, focusedEdgeId, focusedNodeId, height, layoutOverride, mode])
  const omitted = Object.values(display.omittedByType).reduce((sum, count) => sum + count, 0)
  return (
    <div ref={container} data-testid="graph-map" aria-label="资源关系图" style={{ height, width: '100%', border: '1px solid var(--border-soft)', borderRadius: 8, position: 'relative' }}>
      {omitted > 0 && <span className="graph-map__omitted">画布预算：已聚合 {omitted} 个节点</span>}
    </div>
  )
}

/** 判断某节点是否为聚焦节点的邻居（用于画布弱化投影）。 */
function isNeighbor(display: { edges: Array<{ source: string; target: string }> }, focusedId: string, candidateId: string): boolean {
  return display.edges.some((edge) => (edge.source === focusedId && edge.target === candidateId) || (edge.target === focusedId && edge.source === candidateId))
}
