// TopologyGraph — react-flow tiered layout visualisation, aligned with ongrid.
// Dagre lays out nodes top-to-bottom then snaps each node onto a horizontal
// band by its type-tier (app top → rack bottom). Node fill keyed on type,
// edge stroke keyed on RelationType.semantics_tag (hard_dep red, observation
// blue, ...). Augmented with per-node trace micro-metrics (health dot + error
// rate + latency) and hover link highlighting.
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  Edge,
  Handle,
  MiniMap,
  Node,
  NodeChange,
  NodeProps,
  Position,
  ReactFlow,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from '@dagrejs/dagre'
import type {
  TopologyNodeItem,
  TopologyRelationItem,
  TopologyRelationTypeItem,
} from '../../api/client'

const NODE_WIDTH = 192
const NODE_HEIGHT = 86
const HANDLE_TARGET_TOP = 'target-top'
const HANDLE_TARGET_BOTTOM = 'target-bottom'
const HANDLE_SOURCE_TOP = 'source-top'
const HANDLE_SOURCE_BOTTOM = 'source-bottom'
const HIDDEN_HANDLE_STYLE = { visibility: 'hidden' as const }
// 每条边在哪个 handle 槽位（用于平行边错开）。最多 4 条平行边。
const MAX_PARALLEL = 4
const handleSlot = (base: string, idx: number) => `${base}__${idx}`

type NodeColor = { bg: string; border: string; fg: string }
// NODE_COLORS：bg 全部提亮至对比度 ≥3.0（vs 画布 #09090b），同时保留类型语义色作边框。
const NODE_COLORS: Record<string, NodeColor> = {
  device: { bg: '#64748b', border: '#cbd5e1', fg: '#0f172a' },
  network_device: { bg: '#0891b2', border: '#22d3ee', fg: '#ecfeff' },
  service: { bg: '#4f46e5', border: '#a5b4fc', fg: '#f8fafc' },
  cluster: { bg: '#047857', border: '#6ee7b7', fg: '#ecfdf5' },
  app: { bg: '#ea580c', border: '#fdba74', fg: '#fff7ed' },
  rack: { bg: '#71717a', border: '#d4d4d8', fg: '#fafafa' },
}
const NODE_COLORS_FALLBACK: NodeColor = { bg: '#64748b', border: '#cbd5e1', fg: '#f8fafc' }
const MINIMAP_NODE_COLORS: Record<string, string> = {
  device: '#60a5fa',
  network_device: '#22d3ee',
  service: '#818cf8',
  cluster: '#34d399',
  app: '#fb923c',
  rack: '#d4d4d8',
}
const EDGE_COLORS: Record<string, string> = {
  hard_dep: '#f87171',
  runtime_dep: '#fb923c',
  traffic: '#fbbf24',
  redundancy: '#34d399',
  observation: '#60a5fa',
  aggregation: '#a78bfa',
  annotation: '#6b7280',
}
const EDGE_DASH: Record<string, string | undefined> = {
  hard_dep: undefined,
  traffic: undefined,
  runtime_dep: '6 3',
  aggregation: '5 4',
  redundancy: '8 3 2 3',
  observation: '2 4',
  annotation: '2 4',
}
// Per node-type tier. Lower = higher in the vertical stack.
const TYPE_TIER: Record<string, number> = {
  app: 0,
  service: 1,
  cluster: 2,
  network_device: 3,
  device: 4,
  rack: 5,
}
const TIER_BAND_HEIGHT = NODE_HEIGHT + 120
const NODE_X_SPACING = NODE_WIDTH + 28

// Health color for the per-node micro-metric dot.
const HEALTH_DOT_COLOR: Record<string, string> = {
  healthy: '#2f9e5f',
  warning: '#d98b1f',
  error: '#e0455b',
}

// 层级中文名映射（面向非专业用户，避免专业术语）。
const TIER_LABEL_CN: Record<string, string> = {
  app: '业务入口',
  service: '核心服务',
  cluster: '数据存储',
  network_device: '网络设备',
  device: '基础设施',
  rack: '基础设施',
}
export function typeTierLabel(type: string): string {
  return TIER_LABEL_CN[type] ?? '其他'
}

// Trace micro-metrics merged per node name (from /topology/global).
export type NodeMetric = {
  error_rate: number
  latency_ms: number
  health: string
  health_score: number
}

function semanticsForType(relTypes: TopologyRelationTypeItem[], typeName: string): string {
  const rt = relTypes.find((t) => t.name === typeName)
  return rt?.semantics_tag ?? 'annotation'
}
function isNetworkDevice(node: TopologyNodeItem | undefined): boolean {
  return node?.type === 'device' && (node.props_json?.includes('"network"') ?? false)
}
function visualNodeType(node: TopologyNodeItem | undefined): string {
  return isNetworkDevice(node) ? 'network_device' : (node?.type ?? '')
}
function nodeTier(node: TopologyNodeItem | undefined): number {
  return TYPE_TIER[visualNodeType(node)] ?? 99
}

function CustomTopologyNode(props: NodeProps) {
  const data = props.data as {
    label: string
    type: string
    selected: boolean
    hoverActive: boolean
    hoverRelated: boolean
    globalHovering: boolean
    abnormal: boolean
    colors: NodeColor
    selectionRing: string
    metric?: NodeMetric
  }
  const colors = data.colors
  const m = data.metric
  const hasMetric = !!m && m.health !== 'unknown' && !!(m.error_rate !== undefined)
  // dimmed：仅在用户正在 hover 某个节点时，才淡化非目标、非邻居节点
  const dimmed = data.globalHovering && !data.hoverActive && !data.hoverRelated
  const borderColor = data.abnormal ? '#ff4d4f' : (data.selected ? data.selectionRing : (data.hoverActive ? '#ffffff' : colors.border))
  return (
    <div
      style={{
        width: NODE_WIDTH,
        height: NODE_HEIGHT,
        background: colors.bg,
        border: `1.5px solid ${borderColor}`,
        borderRadius: 8,
        padding: '5px 10px',
        boxShadow: data.hoverActive
          ? `0 0 0 2px rgba(255,255,255,0.85), 0 0 14px rgba(255,255,255,0.5)`
          : (data.abnormal
              ? '0 0 0 2px rgba(255,77,79,0.45), 0 0 14px rgba(255,77,79,0.6)'
              : (data.selected ? `0 0 0 2px ${data.selectionRing}55` : undefined)),
        color: colors.fg,
        fontSize: 11,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        overflow: 'hidden',
        cursor: 'pointer',
        opacity: dimmed ? 0.12 : 1,
        transition: 'opacity 0.15s',
      }}
    >
      {/* 多 handle 槽位：每条平行边从不同位置出发，形成清晰平行通道 */}
      {Array.from({ length: MAX_PARALLEL }).map((_, i) => (
        <Handle
          key={`tt-${i}`}
          id={handleSlot(HANDLE_TARGET_TOP, i)}
          type="target"
          position={Position.Top}
          style={{ ...HIDDEN_HANDLE_STYLE, left: `${((i + 1) / (MAX_PARALLEL + 1)) * 100}%` }}
        />
      ))}
      {Array.from({ length: MAX_PARALLEL }).map((_, i) => (
        <Handle
          key={`st-${i}`}
          id={handleSlot(HANDLE_SOURCE_TOP, i)}
          type="source"
          position={Position.Top}
          style={{ ...HIDDEN_HANDLE_STYLE, left: `${((i + 1) / (MAX_PARALLEL + 1)) * 100}%` }}
        />
      ))}
      <div style={{ fontWeight: 600, fontSize: 14, lineHeight: 1.35, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
        {data.label}
      </div>
      <div style={{ fontSize: 11, opacity: 0.55, display: 'flex', alignItems: 'center', gap: 6, marginTop: 2 }}>
        <span>{typeTierLabel(data.type)}</span>
        {hasMetric && (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, marginLeft: 'auto' }}>
            <span style={{ width: 8, height: 8, borderRadius: '50%', background: HEALTH_DOT_COLOR[m.health] ?? '#5b6b8c', display: 'inline-block', boxShadow: `0 0 6px ${HEALTH_DOT_COLOR[m.health] ?? '#5b6b8c'}` }} />
            <span style={{ color: m.error_rate > 0 ? '#ff6b6b' : 'rgba(255,255,255,0.6)', fontWeight: 500 }}>
              {m.error_rate > 0 ? `${m.error_rate.toFixed(1)}%` : '正常'}
            </span>
          </span>
        )}
      </div>
      {Array.from({ length: MAX_PARALLEL }).map((_, i) => (
        <Handle
          key={`tb-${i}`}
          id={handleSlot(HANDLE_TARGET_BOTTOM, i)}
          type="target"
          position={Position.Bottom}
          style={{ ...HIDDEN_HANDLE_STYLE, left: `${((i + 1) / (MAX_PARALLEL + 1)) * 100}%` }}
        />
      ))}
      {Array.from({ length: MAX_PARALLEL }).map((_, i) => (
        <Handle
          key={`sb-${i}`}
          id={handleSlot(HANDLE_SOURCE_BOTTOM, i)}
          type="source"
          position={Position.Bottom}
          style={{ ...HIDDEN_HANDLE_STYLE, left: `${((i + 1) / (MAX_PARALLEL + 1)) * 100}%` }}
        />
      ))}
    </div>
  )
}
const nodeTypes = { topo: CustomTopologyNode }

type Props = {
  nodes: TopologyNodeItem[]
  relations: TopologyRelationItem[]
  relationTypes: TopologyRelationTypeItem[]
  selectedName?: string | null
  hideOrphans?: boolean
  visibleRelationTypes?: Set<string>
  metrics?: Record<string, NodeMetric>
  onlyAbnormal?: boolean
  abnormalNames?: Set<string>
  focusNodeId?: number | null
  onSelect(node: TopologyNodeItem): void
}

export function TopologyGraph({
  nodes,
  relations,
  relationTypes,
  selectedName,
  hideOrphans,
  visibleRelationTypes,
  metrics,
  onlyAbnormal,
  abnormalNames,
  focusNodeId,
  onSelect,
}: Props) {
  const [hoveredID, setHoveredID] = useState<string | null>(null)
  const rfInstanceRef = React.useRef<any>(null)
  const { rfNodes: layoutNodes, rfEdges } = useMemo(
    () =>
      layoutGraph(
        nodes,
        relations,
        relationTypes,
        selectedName,
        hideOrphans ?? false,
        visibleRelationTypes,
        metrics,
        hoveredID,
        onlyAbnormal,
        abnormalNames,
      ),
    [nodes, relations, relationTypes, selectedName, hideOrphans, visibleRelationTypes, metrics, hoveredID, onlyAbnormal, abnormalNames],
  )
  const [draggedPositions, setDraggedPositions] = useState<Record<string, { x: number; y: number }>>({})
  const rfNodes = useMemo(
    () => layoutNodes.map((node) => ({ ...node, position: draggedPositions[node.id] ?? node.position })),
    [layoutNodes, draggedPositions],
  )
  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setDraggedPositions((previous) => {
      const next = { ...previous }
      for (const change of changes) {
        if (change.type === 'position' && change.position) next[change.id] = change.position
        if (change.type === 'remove') delete next[change.id]
      }
      return next
    })
  }, [])

  useEffect(() => {
    const t = setTimeout(() => window.dispatchEvent(new Event('resize')), 50)
    return () => clearTimeout(t)
  }, [])

  // 聚焦指定节点（默认聚焦最新告警/异常节点）
  useEffect(() => {
    if (focusNodeId == null || !rfInstanceRef.current) return
    const t = setTimeout(() => {
      rfInstanceRef.current.fitView({ nodes: [{ id: String(focusNodeId) }], padding: 0.3, maxZoom: 1.4, duration: 500 })
    }, 80)
    return () => clearTimeout(t)
  }, [focusNodeId])

  // 图例数据：实际出现的节点类型 + 边语义色
  const presentTypes = useMemo(() => {
    const set = new Set<string>()
    for (const n of nodes) set.add(visualNodeType(n))
    return set
  }, [nodes])
  const presentTags = useMemo(() => {
    const set = new Set<string>()
    for (const r of relations) set.add(semanticsForType(relationTypes, r.type))
    return set
  }, [relations, relationTypes])

  const legendData = [
    ...['app', 'service', 'cluster', 'network_device', 'device', 'rack']
      .filter((t) => presentTypes.has(t))
      .map((t) => ({ label: typeTierLabel(t), color: NODE_COLORS[t]?.border ?? '#3f3f46' })),
    ...Array.from(presentTags)
      .filter((tag) => EDGE_COLORS[tag])
      .map((tag) => ({ label: tag, color: EDGE_COLORS[tag] })),
  ]

  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        nodesDraggable
        nodesConnectable={false}
        elementsSelectable
        proOptions={{ hideAttribution: true }}
        fitView
        fitViewOptions={{ padding: 0.08, minZoom: 0.5, maxZoom: 1.2 }}
        minZoom={0.3}
        maxZoom={2.5}
        onNodeClick={(_, n) => {
          const orig = nodes.find((x) => String(x.id) === n.id)
          if (orig) onSelect(orig)
        }}
        onNodeMouseEnter={(_, n) => setHoveredID(n.id)}
        onNodeMouseLeave={() => setHoveredID(null)}
        onInit={(inst) => { rfInstanceRef.current = inst }}
        style={{ background: '#09090b' }}
      >
        <Background variant={BackgroundVariant.Dots} color="#27272a" gap={20} />
        <MiniMap
          nodeColor={(n) => MINIMAP_NODE_COLORS[(n.data as { type: string }).type] ?? '#a1a1aa'}
          nodeStrokeColor="rgba(0,0,0,0.4)"
          nodeStrokeWidth={2}
          nodeBorderRadius={3}
          pannable
          zoomable
        />
        <Controls showInteractive={false} />
      </ReactFlow>
      {/* 图例面板：节点类型颜色 + 边语义色 */}
      {legendData.length > 0 && (
        <div
          style={{
            position: 'absolute',
            left: 12,
            bottom: 12,
            background: 'rgba(9,9,11,0.85)',
            border: '1px solid rgba(255,255,255,0.12)',
            borderRadius: 8,
            padding: '8px 12px',
            display: 'flex',
            flexWrap: 'wrap',
            gap: '4px 14px',
            maxWidth: 320,
            zIndex: 5,
            pointerEvents: 'none',
          }}
        >
          {legendData.map((lg) => (
            <span key={lg.label} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 11, color: 'rgba(255,255,255,0.75)' }}>
              <span
                style={{
                  width: 10,
                  height: 10,
                  borderRadius: 2,
                  background: presentTags.has(lg.label) ? 'none' : lg.color,
                  border: presentTags.has(lg.label) ? `2px solid ${lg.color}` : 'none',
                }}
              />
              {lg.label}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}

// layoutGraph runs dagre (TB) for X positions, then snaps Y to type-tier bands.
function layoutGraph(
  nodes: TopologyNodeItem[],
  relations: TopologyRelationItem[],
  relationTypes: TopologyRelationTypeItem[],
  selectedName: string | null | undefined,
  hideOrphans: boolean,
  visibleRelationTypes?: Set<string>,
  metrics?: Record<string, NodeMetric>,
  hoveredID?: string | null,
  onlyAbnormal?: boolean,
  abnormalNames?: Set<string>,
): { rfNodes: Node[]; rfEdges: Edge[] } {
  const selectionRing = '#ffffff'
  const includedRelations = relations.filter((r) => !visibleRelationTypes || visibleRelationTypes.has(r.type))

  const allIDs = new Set<number>()
  for (const n of nodes) allIDs.add(n.id)

  const referenced = new Set<number>()
  for (const r of includedRelations) {
    if (allIDs.has(r.src_id) && allIDs.has(r.dst_id)) {
      referenced.add(r.src_id)
      referenced.add(r.dst_id)
    }
  }
  let visibleNodes = hideOrphans ? nodes.filter((n) => referenced.has(n.id)) : nodes
  if (onlyAbnormal && abnormalNames) {
    // 只看异常：异常节点 + 其直接邻居（保留链路上下文）
    const abnormalSet = new Set<string>()
    for (const n of nodes) if (abnormalNames.has(n.name)) abnormalSet.add(String(n.id))
    const keep = new Set<string>(abnormalSet)
    for (const r of includedRelations) {
      if (abnormalSet.has(String(r.src_id))) keep.add(String(r.dst_id))
      if (abnormalSet.has(String(r.dst_id))) keep.add(String(r.src_id))
    }
    visibleNodes = visibleNodes.filter((n) => keep.has(String(n.id)))
  }
  const visibleNodeIDs = new Set(visibleNodes.map((n) => n.id))

  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'TB', nodesep: 80, ranksep: 60, marginx: 40, marginy: 40 })
  for (const n of visibleNodes) g.setNode(String(n.id), { width: NODE_WIDTH, height: NODE_HEIGHT })
  for (const r of includedRelations) {
    if (!visibleNodeIDs.has(r.src_id) || !visibleNodeIDs.has(r.dst_id)) continue
    g.setEdge(String(r.src_id), String(r.dst_id))
  }
  dagre.layout(g)

  // Tier snap
  const byTier = new Map<number, TopologyNodeItem[]>()
  for (const n of visibleNodes) {
    const tier = nodeTier(n)
    const bucket = byTier.get(tier) ?? []
    bucket.push(n)
    byTier.set(tier, bucket)
  }
  const tierLayout = new Map<number, { y: number; xs: number[] }>()
  const sortedTiers = [...byTier.keys()].sort((a, b) => a - b)
  let maxRowWidth = 0
  for (const tier of sortedTiers) {
    const bucket = byTier.get(tier)!
    bucket.sort((a, b) => (g.node(String(a.id))?.x ?? 0) - (g.node(String(b.id))?.x ?? 0))
    const rowWidth = bucket.length * NODE_X_SPACING
    if (rowWidth > maxRowWidth) maxRowWidth = rowWidth
  }
  sortedTiers.forEach((tier, tierIdx) => {
    const bucket = byTier.get(tier)!
    const rowWidth = bucket.length * NODE_X_SPACING
    const startX = 40 + (maxRowWidth - rowWidth) / 2
    const xs = bucket.map((_, i) => startX + i * NODE_X_SPACING)
    tierLayout.set(tier, { y: 40 + tierIdx * TIER_BAND_HEIGHT, xs })
  })
  const positionFor = new Map<number, { x: number; y: number }>()
  for (const tier of sortedTiers) {
    const bucket = byTier.get(tier)!
    const { y, xs } = tierLayout.get(tier)!
    bucket.forEach((n, i) => positionFor.set(n.id, { x: xs[i], y }))
  }

  // Hover: which edges/neighbours touch the hovered node? Used to highlight/ dim.
  const hoveredNum = hoveredID ? Number(hoveredID) : null
  const hoverRelatedEdge = new Set<string>()
  const hoverNeighborIDs = new Set<number>()
  if (hoveredNum != null) {
    for (const r of includedRelations) {
      if (r.src_id === hoveredNum || r.dst_id === hoveredNum) {
        hoverRelatedEdge.add(String(r.id))
        hoverNeighborIDs.add(r.src_id)
        hoverNeighborIDs.add(r.dst_id)
      }
    }
  }

  const rfNodes: Node[] = visibleNodes.map((n) => {
    const pos = positionFor.get(n.id) ?? { x: 0, y: 0 }
    const vtype = visualNodeType(n)
    const isHoverActive = hoveredNum === n.id
    const isHoverRelated = isHoverActive || hoverNeighborIDs.has(n.id)
    const isAbnormal = abnormalNames?.has(n.name) ?? false
    return {
      id: String(n.id),
      type: 'topo',
      position: pos,
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      data: {
        label: n.name,
        type: vtype,
        selected: selectedName === n.name,
        hoverActive: isHoverActive,
        hoverRelated: isHoverRelated,
        globalHovering: hoveredNum != null,  // 是否有节点正在被 hover
        abnormal: isAbnormal,
        colors: NODE_COLORS[vtype] ?? NODE_COLORS_FALLBACK,
        selectionRing,
        metric: metrics?.[n.name],
      },
    }
  })

  const seenPairs = new Set<string>()
  const nodeByID = new Map(visibleNodes.map((n) => [n.id, n]))
  // 每条平行边（同 src→dst 同方向）分配一个 handle 槽位，让它们从节点不同位置出发形成平行通道
  const parallelIndex = new Map<string, number>()
  const parallelEdges = includedRelations.filter(
    (r) => visibleNodeIDs.has(r.src_id) && visibleNodeIDs.has(r.dst_id),
  )
  const rfEdges: Edge[] = parallelEdges.map((r) => {
    const tag = semanticsForType(relationTypes, r.type)
    const src = nodeByID.get(r.src_id)
    const dst = nodeByID.get(r.dst_id)
    // 故障链路标红：异常节点关联的边统一红色
    const edgeAbnormal = (abnormalNames?.has(src?.name ?? '') ?? false) || (abnormalNames?.has(dst?.name ?? '') ?? false)
    const stroke = edgeAbnormal ? '#ff4d4f' : (EDGE_COLORS[tag] ?? '#52525b')
    const dash = EDGE_DASH[tag]
    const isSel = selectedName === src?.name || selectedName === dst?.name
    const pairKey = `${r.src_id}->${r.dst_id}`
    const showLabel = !seenPairs.has(pairKey)
    seenPairs.add(pairKey)
    const srcTier = nodeTier(src)
    const dstTier = nodeTier(dst)
    const pointsUp = srcTier > dstTier // src 在更高 tier → 边从 src 顶部连出，进 dst 底部
    const isRelated = hoveredNum == null || hoverRelatedEdge.has(String(r.id))
    const hovering = hoveredNum != null
    let opacity = hovering ? (isRelated ? 1 : 0.05) : 0.85
    if (selectedName && !isSel) opacity = 0.25
    const strokeWidth = isSel ? 2.5 : (hovering && isRelated ? 3.5 : 1.4)
    // 平行边槽位：同方向第 N 条边走第 N 个 handle，在节点边缘均匀排布
    const pid = pairKey + (pointsUp ? '^' : 'v')
    const idx = Math.min(parallelIndex.get(pid) ?? 0, MAX_PARALLEL - 1)
    parallelIndex.set(pid, idx + 1)
    const sourceHandle = pointsUp ? handleSlot(HANDLE_SOURCE_TOP, idx) : handleSlot(HANDLE_SOURCE_BOTTOM, idx)
    const targetHandle = pointsUp ? handleSlot(HANDLE_TARGET_BOTTOM, idx) : handleSlot(HANDLE_TARGET_TOP, idx)
    return {
      id: `rel-${r.id}`,
      source: String(r.src_id),
      target: String(r.dst_id),
      sourceHandle,
      targetHandle,
      type: 'smoothstep',
      animated: isRelated && hovering,
      label: showLabel ? r.type : undefined,
      labelStyle: { fill: stroke, fontSize: 10, fontFamily: 'monospace' },
      labelBgStyle: { fill: '#09090b', fillOpacity: 0.9 },
      labelBgPadding: [4, 2] as [number, number],
      labelBgBorderRadius: 3,
      labelShowBg: true,
      style: {
        stroke,
        strokeWidth,
        strokeDasharray: dash,
        opacity,
        filter: hovering && isRelated ? `drop-shadow(0 0 4px ${stroke})` : undefined,
      },
      markerEnd: { type: 'arrowclosed', color: stroke, width: 18, height: 18 } as Edge['markerEnd'],
    }
  })

  return { rfNodes, rfEdges }
}
