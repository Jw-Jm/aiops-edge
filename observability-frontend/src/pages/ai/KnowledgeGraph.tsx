import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Select, Spin, Space, Tag, Segmented, Table, Input, Switch, type TableColumnsType } from 'antd'
import * as echarts from 'echarts'
import { getKgGraph, KgNode, KgEdge, KgGraph } from '../../api/client'
import { PageHeader, Breadcrumb, Empty, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

// 节点类型 → 颜色（P2-4 规格：service 蓝 / instance 青 / node 绿 / pod 紫 / middleware 橙 / server 红 / switch 深灰）
const TYPE_COLORS: Record<string, string> = {
  service: '#1677ff',
  instance: '#13c2c2',
  node: '#52c41a',
  pod: '#722ed1',
  middleware: '#fa8c16',
  server: '#f5222d',
  switch: '#595959',
  cluster: '#2f54eb',
  other: '#8c8c8c',
}
// 列表视图类型 Tag 用 antd 预设色（可读性优于纯色填充）
const TYPE_TAG_PRESET: Record<string, string> = {
  service: 'blue',
  instance: 'cyan',
  node: 'green',
  pod: 'purple',
  middleware: 'orange',
  server: 'red',
  switch: 'default',
  cluster: 'geekblue',
  other: 'default',
}
const CATEGORIES = Object.entries(TYPE_COLORS).map(([name, color]) => ({
  name,
  itemStyle: { color, borderColor: '#fff', borderWidth: 1 },
}))
// ECharts graph 节点 category 字段需要 categories 数组的数字索引（不是名字）
const CAT_INDEX: Record<string, number> = Object.fromEntries(CATEGORIES.map((c, i) => [c.name, i]))

// 实线边类型（调用/依赖/包含等强关系用实线，其余用虚线区分）
const SOLID_EDGE_TYPES = ['calls', 'call', 'depends', 'depends_on', 'dependency', 'belongs', 'contains', 'contain',
  'runs', 'run', 'deployed_on', 'deploy', 'use', 'uses', 'manage', 'connected']

function edgeType(e: KgEdge): 'solid' | 'dashed' {
  const t = String(e?.type || '').toLowerCase()
  if (!t) return 'solid'
  return SOLID_EDGE_TYPES.includes(t) ? 'solid' : 'dashed'
}

const normType = (t?: string): string => String(t || 'other').toLowerCase()

// 依赖/调用类边（用于图谱视图"仅服务依赖拓扑"过滤 + 列表服务调用量聚合）
const isDepEdge = (t: string) =>
  t === 'depends_on' || t === 'depends' || t === 'dependency' || t === 'calls' || t === 'call'

// 数据指纹：30s 轮询时若节点/边身份未变则跳过 setState，避免 force 布局重启致节点乱飘
const graphFp = (g: KgGraph | undefined | null): string => {
  const ns = (g?.nodes || []).map((n) => `${n.id ?? n.name}|${n.type || ''}`).sort().join(';')
  const es = (g?.edges || g?.links || []).map((e) =>
    `${e.src ?? e.source}|${e.dst ?? e.target}|${e.type || ''}|${e.props?.calls ?? e.calls ?? e.value ?? 0}`,
  ).sort().join(';')
  return `${ns}||${es}`
}

// ===== 列表视图：树形数据结构 =====
interface TreeRow {
  key: string
  name: string
  type: string
  calls?: number
  errors?: number
  podCount?: number
  nodeCount?: number
  serviceCount?: number
  parent?: string        // 归属路径（仅 flat 模式填充，用于非"全部"类型筛选时显示上下文）
  props?: Record<string, unknown>
  children?: TreeRow[]
}

// 从图谱 {nodes, edges} 构建"集群 → 节点 → 服务"三级树 + 扁平行（供类型筛选）。
// 层级边语义：
//   CONTAINS : cluster → node（集群包含节点）
//   RUNS_ON  : pod → node（Pod 跑在节点上）/ service → pod（服务跑在 Pod 上）
// 降级：后端尚未补齐 RUNS_ON/CONTAINS 边时，服务无法归到节点/集群，
//   统一落入"未关联节点"伪集群分组，不报错。
function buildClusterTree(graph: KgGraph | undefined | null) {
  const nodes: KgNode[] = graph?.nodes || []
  const edges: KgEdge[] = graph?.edges || graph?.links || []
  const nodeById = new Map<string, KgNode>()
  nodes.forEach((n) => nodeById.set(String(n.id ?? n.name), n))
  const typeOf = (id: string) => normType(nodeById.get(id)?.type)
  const nameOf = (id: string) => nodeById.get(id)?.name || id

  // 层级映射
  const nodeToCluster = new Map<string, string>()   // nodeId -> clusterId
  const podToNode = new Map<string, string>()       // podId -> nodeId
  const serviceToPod = new Map<string, string>()    // serviceId -> podId
  const serviceToNodeDirect = new Map<string, string>() // serviceId -> nodeId（无 pod 中介时）

  for (const e of edges) {
    const sId = String(e.src ?? e.source ?? '')
    const tId = String(e.dst ?? e.target ?? '')
    if (!sId || !tId || sId === 'undefined' || tId === 'undefined') continue
    const t = normType(e.type)
    const sType = typeOf(sId), tType = typeOf(tId)
    if (t === 'contains' || t === 'contain' || t === 'belongs' || t === 'belong') {
      // cluster → node（包含关系，src 为容器）
      nodeToCluster.set(tId, sId)
    } else if (t === 'runs_on' || t === 'runs' || t === 'run' || t === 'deployed_on' || t === 'deploy') {
      if (sType === 'pod' && tType === 'node') podToNode.set(sId, tId)
      else if (sType === 'service' && tType === 'pod') serviceToPod.set(sId, tId)
      else if (sType === 'service' && tType === 'node') serviceToNodeDirect.set(sId, tId)
      // 类型未知时按 dst 类型兜底归类，尽量不丢层级
      else if (tType === 'node') podToNode.set(sId, tId)
      else if (tType === 'pod') serviceToPod.set(sId, tId)
    }
  }

  // service → node（优先经 pod 链路，其次直连）
  const serviceToNode = new Map<string, string>()
  for (const [svc, pod] of serviceToPod) {
    const node = podToNode.get(pod)
    if (node) serviceToNode.set(svc, node)
  }
  for (const [svc, node] of serviceToNodeDirect) {
    if (!serviceToNode.has(svc)) serviceToNode.set(svc, node)
  }

  // 每服务的调用量/错误数（从 DEPENDS_ON/calls 边 props 聚合，按 src 累加）
  const svcCalls = new Map<string, number>()
  const svcErrors = new Map<string, number>()
  for (const e of edges) {
    const t = normType(e.type)
    if (isDepEdge(t)) {
      const sId = String(e.src ?? e.source ?? '')
      const calls = Number(e.props?.calls ?? e.calls ?? e.value) || 0
      const errors = Number(e.props?.errors ?? e.errors) || 0
      svcCalls.set(sId, (svcCalls.get(sId) || 0) + calls)
      svcErrors.set(sId, (svcErrors.get(sId) || 0) + errors)
    }
  }

  const clusters = nodes.filter((n) => normType(n.type) === 'cluster')
  const k8sNodes = nodes.filter((n) => normType(n.type) === 'node')
  const services = nodes.filter((n) => normType(n.type) === 'service')
  const pods = nodes.filter((n) => normType(n.type) === 'pod')

  const clusterToNodes = new Map<string, string[]>()
  for (const [nodeId, clusterId] of nodeToCluster) {
    if (!clusterToNodes.has(clusterId)) clusterToNodes.set(clusterId, [])
    clusterToNodes.get(clusterId)!.push(nodeId)
  }
  const nodeToPods = new Map<string, string[]>()
  for (const [podId, nodeId] of podToNode) {
    if (!nodeToPods.has(nodeId)) nodeToPods.set(nodeId, [])
    nodeToPods.get(nodeId)!.push(podId)
  }
  const nodeToServices = new Map<string, string[]>()
  for (const [svcId, nodeId] of serviceToNode) {
    if (!nodeToServices.has(nodeId)) nodeToServices.set(nodeId, [])
    nodeToServices.get(nodeId)!.push(svcId)
  }

  const usedServiceIds = new Set<string>()

  const mkServiceRow = (svcId: string, keyPrefix: string): TreeRow | null => {
    const svc = nodeById.get(svcId)
    if (!svc) return null
    usedServiceIds.add(svcId)
    return {
      key: `${keyPrefix}-svc-${svcId}`,
      name: svc.name,
      type: 'service',
      calls: svcCalls.get(svcId) || 0,
      errors: svcErrors.get(svcId) || 0,
      props: (svc.props as Record<string, unknown>) || {},
    }
  }
  const mkNodeRow = (nodeId: string, keyPrefix: string): TreeRow | null => {
    const node = nodeById.get(nodeId)
    if (!node) return null
    const podIds = nodeToPods.get(nodeId) || []
    const svcIds = nodeToServices.get(nodeId) || []
    const svcChildren = svcIds
      .map((sid) => mkServiceRow(sid, `${keyPrefix}-${nodeId}`))
      .filter(Boolean) as TreeRow[]
    return {
      key: `${keyPrefix}-node-${nodeId}`,
      name: node.name,
      type: 'node',
      podCount: podIds.length,
      serviceCount: svcIds.length,
      props: (node.props as Record<string, unknown>) || {},
      children: svcChildren,
    }
  }

  // 一级：集群
  const tree: TreeRow[] = []
  for (const cluster of clusters) {
    const cId = String(cluster.id ?? cluster.name)
    const nodeIds = clusterToNodes.get(cId) || []
    const children = nodeIds.map((nid) => mkNodeRow(nid, cId)).filter(Boolean) as TreeRow[]
    const svcTotal = nodeIds.reduce((a, nid) => a + (nodeToServices.get(nid)?.length || 0), 0)
    tree.push({
      key: `cluster-${cId}`,
      name: cluster.name,
      type: 'cluster',
      nodeCount: nodeIds.length,
      serviceCount: svcTotal,
      props: (cluster.props as Record<string, unknown>) || {},
      children,
    })
  }

  // 降级分组：未挂到任何集群的节点 + 未挂到任何节点的服务 → "未关联节点"
  const orphanNodeIds = k8sNodes
    .filter((n) => !nodeToCluster.has(String(n.id ?? n.name)))
    .map((n) => String(n.id ?? n.name))
  const orphanChildren: TreeRow[] = []
  for (const nodeId of orphanNodeIds) {
    const row = mkNodeRow(nodeId, 'orphan')
    if (row) orphanChildren.push(row)
  }
  const remainingOrphanServices = services.filter((s) => !usedServiceIds.has(String(s.id ?? s.name)))
  for (const svc of remainingOrphanServices) {
    const row = mkServiceRow(String(svc.id ?? svc.name), 'orphan-flat')
    if (row) orphanChildren.push(row)
  }
  if (orphanChildren.length > 0) {
    tree.push({
      key: 'cluster-__orphan__',
      name: '未关联节点',
      type: 'cluster',
      nodeCount: orphanNodeIds.length,
      serviceCount: remainingOrphanServices.length +
        orphanNodeIds.reduce((a, nid) => a + (nodeToServices.get(nid)?.length || 0), 0),
      props: {},
      children: orphanChildren,
    })
  }

  // 扁平行（供类型筛选：全部/集群/节点/服务/Pod），带归属路径
  const flatRows: TreeRow[] = nodes.map((n) => {
    const id = String(n.id ?? n.name)
    const nt = normType(n.type)
    let parent = ''
    if (nt === 'service') {
      const node = serviceToNode.get(id)
      const cluster = node ? nodeToCluster.get(node) : undefined
      parent = [cluster ? nameOf(cluster) : '', node ? nameOf(node) : ''].filter(Boolean).join(' / ')
    } else if (nt === 'node') {
      const cluster = nodeToCluster.get(id)
      parent = cluster ? nameOf(cluster) : ''
    } else if (nt === 'pod') {
      const node = podToNode.get(id)
      parent = node ? nameOf(node) : ''
    }
    return {
      key: `flat-${id}`,
      name: n.name,
      type: nt,
      calls: nt === 'service' ? (svcCalls.get(id) || 0) : undefined,
      errors: nt === 'service' ? (svcErrors.get(id) || 0) : undefined,
      podCount: nt === 'node' ? (nodeToPods.get(id)?.length || 0) : undefined,
      nodeCount: nt === 'cluster' ? (clusterToNodes.get(id)?.length || 0) : undefined,
      serviceCount: nt === 'cluster'
        ? (clusterToNodes.get(id) || []).reduce((a, nid) => a + (nodeToServices.get(nid)?.length || 0), 0)
        : undefined,
      parent,
      props: (n.props as Record<string, unknown>) || {},
    }
  })

  return {
    tree,
    flatRows,
    stats: {
      clusters: clusters.length,
      nodes: k8sNodes.length,
      services: services.length,
      pods: pods.length,
      edges: edges.length,
    },
  }
}

// 按名称搜索过滤树：自身命中则保留整棵子树；否则仅保留命中后代的分支
function filterTreeBySearch(rows: TreeRow[], q: string): TreeRow[] {
  if (!q) return rows
  const ql = q.toLowerCase()
  const rec = (list: TreeRow[]): TreeRow[] => {
    const out: TreeRow[] = []
    for (const r of list) {
      if (r.name.toLowerCase().includes(ql)) {
        out.push(r)
      } else if (r.children) {
        const kids = rec(r.children)
        if (kids.length > 0) out.push({ ...r, children: kids })
      }
    }
    return out
  }
  return rec(rows)
}

// 行状态：服务按错误数推导；其余取 props.status（Running/Ready→正常，Error/Fail→异常）
function statusOf(r: TreeRow): string {
  if (r.type === 'service') {
    if ((r.errors || 0) > 0) return '异常'
    if ((r.calls || 0) > 0) return '正常'
    return '未知'
  }
  const s = String((r.props as Record<string, unknown>)?.status || '').toLowerCase()
  if (!s) return '未知'
  if (['running', 'ready', 'ok', 'normal', 'active', 'healthy'].includes(s)) return '正常'
  if (['error', 'fail', 'failed', 'down', 'crash'].includes(s)) return '异常'
  if (['pending', 'waiting', 'warn', 'warning'].includes(s)) return '告警'
  return s
}
function statusToneOf(r: TreeRow): StatusTone {
  const s = statusOf(r)
  if (s === '异常') return 'crit'
  if (s === '正常') return 'ok'
  if (s === '告警') return 'warn'
  if (s === '未知') return 'muted'
  return 'info'
}

const TypeTag: React.FC<{ type: string }> = ({ type }) => (
  <Tag color={TYPE_TAG_PRESET[type] || 'default'} style={{ margin: 0, textTransform: 'capitalize' }}>{type}</Tag>
)

const RowProps: React.FC<{ r: TreeRow }> = ({ r }) => {
  const parts: string[] = []
  if (r.type === 'cluster') parts.push(`${r.nodeCount ?? 0} 节点 · ${r.serviceCount ?? 0} 服务`)
  else if (r.type === 'node') parts.push(`${r.podCount ?? 0} Pod · ${r.serviceCount ?? 0} 服务`)
  if (r.parent) parts.push(`归属 ${r.parent}`)
  const props = (r.props || {}) as Record<string, unknown>
  const extra = Object.keys(props)
    .filter((k) => !['cluster_id', 'created_by', 'status'].includes(k))
    .map((k) => `${k}: ${String(props[k])}`)
  const text = [...parts, ...extra].join(' · ')
  return <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{text || '—'}</span>
}

const KnowledgeGraph: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const setCurrentCluster = useUIStore((s) => s.setCurrentCluster)
  const clusters = useUIStore((s) => s.clusters)
  const refreshClusters = useUIStore((s) => s.refreshClusters)

  const chartRef = useRef<HTMLDivElement>(null)
  const chartInst = useRef<echarts.ECharts | null>(null)
  // 收敛后的稳定节点坐标缓存（id -> {x,y}）。用 ref 不触发重渲染：
  // 30s 轮询数据未变时复用稳定位置，force 不从环形种子重新散开，首次收敛后保持静止。
  const stablePosRef = useRef<Record<string, { x: number; y: number }>>({})
  const [loading, setLoading] = useState(true)
  const [graph, setGraph] = useState<KgGraph>({ nodes: [], edges: [] })

  // 视图切换：图谱视图 / 列表视图（参照 ServiceObservability 的 Segmented 模式）
  const [view, setView] = useState<'graph' | 'list'>('graph')
  // 图谱视图：默认仅服务依赖拓扑，开启后显示全部节点（含 node/pod 等基础设施）
  const [showAll, setShowAll] = useState(false)
  // 列表视图：类型筛选 + 名称搜索
  const [typeFilter, setTypeFilter] = useState<'all' | 'cluster' | 'node' | 'service' | 'pod'>('all')
  const [search, setSearch] = useState('')

  // 集群数据来源：复用 uiStore（与顶栏 ClusterSwitcher 同一份）
  useEffect(() => {
    if (clusters.length === 0) refreshClusters()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 拉取图谱（后端 API 尚未就绪时 client 容错返回空图 + unavailable 标记）
  const loadGraph = (silent = false) => {
    if (!silent) setLoading(true)
    // Bug3 修复：图谱数据后端按 props.cluster_id 标记，当前真实环境统一为 "default"。
    // 集群下拉传的是 kubernetes-cluster 的数字 id（如 1），与图谱 cluster_id 体系不一致，
    // 选中具体集群时传数字 id 会让后端按 cluster_id 过滤返回 0 节点（str("1") != str("default")）。
    // 这里选中具体集群时传 cluster_id="default"（图谱数据实际标签），让图谱可见；
    // "全部集群"保持不传 cluster_id（后端默认 default），行为不变。
    const params: Record<string, unknown> = currentClusterId !== 'all' ? { cluster_id: 'default' } : {}
    getKgGraph(params)
      .then((r) => {
        const next = r.data || { nodes: [], edges: [] }
        // 30s 静默轮询只在数据真正变化时才 setState，避免每次都触发布局重新迭代导致节点持续飘移
        setGraph((prev) => (graphFp(prev) === graphFp(next) ? prev : next))
      })
      .catch(() => setGraph({ nodes: [], edges: [], unavailable: true }))
      .finally(() => { if (!silent) setLoading(false) })
  }
  useEffect(() => {
    loadGraph(false)
    // 30s 静默轮询：图谱每 1 分钟由后端自动重建，前端近实时拉最新图
    const timer = setInterval(() => loadGraph(true), 30000)
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentClusterId])

  // 卸载时销毁 ECharts 实例
  useEffect(() => () => {
    if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
    chartInst.current = null
  }, [])

  // 渲染 graph（自研力导向布局 + 硬性边界约束，参照 ServiceObservability）
  useEffect(() => {
    // 离开图谱视图时 dispose 实例并清空引用，回到图谱视图时重新 init（避免渲染到脱离 DOM 的 canvas）
    if (view !== 'graph') {
      if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
      chartInst.current = null
      return
    }
    if (!chartRef.current) return
    if (!chartInst.current || chartInst.current.isDisposed() || chartInst.current.getDom() !== chartRef.current) {
      if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
      chartInst.current = echarts.init(chartRef.current)
    }
    const inst = chartInst.current

    const rawNodes: KgNode[] = graph?.nodes || []
    const rawEdges: KgEdge[] = graph?.edges || graph?.links || []

    // 图谱视图优化：默认只显示服务节点 + DEPENDS_ON 边（依赖拓扑干净）；
    // 开启"显示全部"后展示全部节点（含 node/pod 等基础设施）与全部边。
    const visibleNodes = showAll
      ? rawNodes
      : rawNodes.filter((n) => normType(n.type) === 'service')
    const visibleEdges = showAll
      ? rawEdges
      : rawEdges.filter((e) => isDepEdge(normType(e.type)))

    // Bug1 修复（节点对齐）：id 用 String(n.id ?? n.name)，与边 src/dst 字符串化后对齐。
    const nodes = visibleNodes.map((n) => {
      const type = normType(n.type)
      return {
        id: String(n.id ?? n.name),
        name: n.name || String(n.id),
        type,
        category: CAT_INDEX[type] ?? CAT_INDEX['other'],
        symbolSize: type === 'service' ? 34 : type === 'switch' ? 30 : 26,
      }
    })
    const validIds = new Set(nodes.map((n) => n.id))
    const idToName: Record<string, string> = {}
    nodes.forEach((n) => { idToName[n.id] = n.name })
    // 节点 props 单独存一份映射，供 tooltip 展示（不塞进 echarts data，避免 echarts 克隆大对象）
    const idToProps: Record<string, Record<string, unknown>> = {}
    visibleNodes.forEach((n) => {
      if (n.props) idToProps[String(n.id ?? n.name)] = n.props as Record<string, unknown>
    })

    // 边宽按调用量缩放（0.7 + 1.3 * value/maxCalls，最大约 2），与 ServiceObservability 对齐。
    const maxCalls = Math.max(1, ...visibleEdges.map((e) => Number(e.props?.calls ?? e.calls ?? e.value) || 1))
    const links = visibleEdges.map((e) => {
      const sId = String(e.src ?? e.source)
      const tId = String(e.dst ?? e.target)
      const calls = Number(e.props?.calls ?? e.calls ?? e.value) || 0
      const errors = Number(e.props?.errors ?? e.errors) || 0
      return {
        source: sId,
        target: tId,
        sourceName: idToName[sId] ?? sId,
        targetName: idToName[tId] ?? tId,
        type: e.type || 'calls',
        calls,
        errors,
        value: calls || 1,
        lineStyle: {
          type: edgeType(e),
          width: 0.7 + 1.3 * (calls || 0) / maxCalls,
          opacity: 0.65,
        },
      }
    }).filter((l) => l.source && l.target && l.source !== 'undefined' && l.target !== 'undefined'
      && validIds.has(l.source) && validIds.has(l.target))

    // Bug2 修复：自研力导向布局 + 硬性边界约束。每轮把节点坐标 clamp 到画布范围内，
    // 从算法层面保证节点永不超出画布。已收敛过的节点用稳定坐标，新节点用环形种子位置。
    const cw = chartRef.current.clientWidth || 800
    const ch = chartRef.current.clientHeight || 500
    const cx = cw / 2, cy = ch / 2, R = Math.min(cw, ch) * 0.32
    const N = Math.max(1, nodes.length)
    const seedPos = nodes.map((n, i) => {
      const prev = stablePosRef.current[n.id]
      if (prev && typeof prev.x === 'number' && typeof prev.y === 'number') return { x: prev.x, y: prev.y }
      return { x: cx + R * Math.cos((2 * Math.PI * i) / N), y: cy + R * Math.sin((2 * Math.PI * i) / N) }
    })
    const PAD = 24
    const PAD_BOTTOM = 60
    const positions: Record<string, { x: number; y: number }> = {}
    nodes.forEach((n, i) => { positions[n.id] = { ...seedPos[i] } })
    const nodeIds = nodes.map((n) => n.id)
    const edgeArr = links.map((l) => ({ s: String(l.source), t: String(l.target) }))
    for (let iter = 0; iter < 300; iter++) {
      // 斥力：所有节点两两排斥
      for (let i = 0; i < nodeIds.length; i++) {
        for (let j = i + 1; j < nodeIds.length; j++) {
          const a = positions[nodeIds[i]], b = positions[nodeIds[j]]
          let dx = a.x - b.x, dy = a.y - b.y
          let d2 = dx * dx + dy * dy
          if (d2 < 1) { dx = (Math.random() - 0.5) * 2; dy = (Math.random() - 0.5) * 2; d2 = 1 }
          const d = Math.sqrt(d2)
          const force = 320 / d2  // 斥力与距离平方成反比
          const fx = (dx / d) * force, fy = (dy / d) * force
          a.x += fx; a.y += fy
          b.x -= fx; b.y -= fy
        }
      }
      // 弹簧力：有边的节点拉到理想距离
      for (const e of edgeArr) {
        const a = positions[e.s], b = positions[e.t]
        if (!a || !b) continue
        let dx = b.x - a.x, dy = b.y - a.y
        const d = Math.max(0.01, Math.sqrt(dx * dx + dy * dy))
        const ideal = 180
        const f = (d - ideal) * 0.02
        a.x += (dx / d) * f; a.y += (dy / d) * f
        b.x -= (dx / d) * f; b.y -= (dy / d) * f
      }
      // 引力：拉到画布中心
      for (const id of nodeIds) {
        const p = positions[id]
        p.x += (cx - p.x) * 0.08
        p.y += (cy - p.y) * 0.08
      }
      // 硬性边界约束：clamp 到画布内
      for (const id of nodeIds) {
        const p = positions[id]
        p.x = Math.max(PAD, Math.min(cw - PAD, p.x))
        p.y = Math.max(PAD, Math.min(ch - PAD_BOTTOM, p.y))
      }
    }
    const dataWithPos = nodes.map((n) => ({
      ...n,
      x: positions[n.id].x, y: positions[n.id].y,
    }))

    try {
      inst.setOption({
        tooltip: {
          trigger: 'item',
          formatter: (p: any) => {
            if (p.dataType === 'edge') {
              const calls = p.data.calls ?? p.data.value ?? 0
              const errors = p.data.errors ?? 0
              return `${p.data.sourceName ?? p.data.source} → ${p.data.targetName ?? p.data.target}<br/>` +
                `<span style="color:#999">${p.data.type || '关系'}</span>` +
                (calls ? `<br/>调用 ${calls} 次${errors ? ` · 错误 ${errors}` : ''}` : '')
            }
            const d = p.data
            const props = idToProps[d.id] || {}
            const extra = Object.keys(props)
              .filter((k) => !['cluster_id', 'created_by'].includes(k))
              .map((k) => `${k}: ${props[k]}`).join(' · ')
            return `<b>${d.name}</b><br/><span style="color:#999">${d.type || 'unknown'}</span>` +
              (extra ? `<br/>${extra}` : '')
          },
        },
        legend: {
          data: CATEGORIES.map((c) => c.name),
          orient: 'horizontal',
          bottom: 0,
          textStyle: { color: '#7a8794', fontSize: 11 },
          itemWidth: 12,
          itemHeight: 12,
        },
        series: [{
          type: 'graph',
          layout: 'none',  // 自研力导向已在上方算好坐标并 clamp 到画布内，layout:'none' 直接用
          roam: true,
          draggable: true,
          animationDurationUpdate: 1200,
          animationDuration: 1200,
          animationEasingUpdate: 'cubicOut',
          data: dataWithPos,
          links,
          categories: CATEGORIES,
          label: {
            show: true, position: 'top', fontSize: 10, color: '#1f2d3d',
            distance: 10, overflow: 'truncate', width: 110, showAbove: true,
          },
          lineStyle: { color: '#c6cfdb', width: 1.2, curveness: 0.22, opacity: 0.7 },
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: [0, 9],
          emphasis: { focus: 'adjacency', lineStyle: { width: 3 }, label: { fontWeight: 600 } },
        }],
      })
    } catch (err) {
      console.error('[KnowledgeGraph] setOption 失败:', err)
    }

    // 保存收敛后稳定坐标到 stablePosRef（只写 ref，不 setState）
    inst.off('finished')
    inst.on('finished', () => {
      try {
        const series = (inst.getOption().series as any[])?.[0]
        const data: any[] = series?.data || []
        for (const d of data) {
          if (d && d.id && typeof d.x === 'number' && typeof d.y === 'number') {
            stablePosRef.current[d.id] = { x: d.x, y: d.y }
          }
        }
      } catch { /* ignore */ }
    })

    const onResize = () => { try { inst.resize() } catch { /* ignore */ } }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [graph, showAll, view])

  const unavailable = graph?.unavailable === true
  const nodeCount = graph?.nodes?.length || 0

  // 列表视图数据：构建树 + 按类型/搜索过滤
  const treeData = useMemo(() => buildClusterTree(graph), [graph])
  const isTree = typeFilter === 'all'
  const displayRows = useMemo(() => {
    if (isTree) return filterTreeBySearch(treeData.tree, search)
    let rows = treeData.flatRows.filter((r) => r.type === typeFilter)
    if (search) {
      const q = search.toLowerCase()
      rows = rows.filter((r) => r.name.toLowerCase().includes(q))
    }
    return rows
  }, [treeData, isTree, typeFilter, search])

  const columns: TableColumnsType<TreeRow> = [
    {
      title: '名称', dataIndex: 'name', key: 'name',
      render: (_, r) => (
        <span style={{ fontWeight: r.type === 'cluster' ? 600 : r.type === 'node' ? 500 : 400 }}>
          {r.name}
        </span>
      ),
    },
    { title: '类型', dataIndex: 'type', key: 'type', width: 92, render: (_, r) => <TypeTag type={r.type} /> },
    {
      title: '状态', key: 'status', width: 88,
      render: (_, r) => <StatusBadge text={statusOf(r)} tone={statusToneOf(r)} />,
    },
    {
      title: '调用量', key: 'calls', width: 96, align: 'right',
      render: (_, r) => r.calls !== undefined
        ? <span>{r.calls.toLocaleString()}</span>
        : <span style={{ color: 'var(--text-muted)' }}>—</span>,
    },
    {
      title: '错误数', key: 'errors', width: 84, align: 'right',
      render: (_, r) => r.errors !== undefined
        ? (r.errors > 0
          ? <span style={{ color: 'var(--danger)', fontWeight: 600 }}>{r.errors}</span>
          : <span style={{ color: 'var(--text-muted)' }}>0</span>)
        : <span style={{ color: 'var(--text-muted)' }}>—</span>,
    },
    { title: '关键属性', key: 'props', render: (_, r) => <RowProps r={r} /> },
  ]

  const clusterOptions = [
    { value: 'all', label: '全部集群' },
    ...clusters.map((c) => ({ value: String(c.id), label: c.name })),
  ]

  const stats = treeData.stats
  const typeFilterLabel: Record<string, string> = {
    all: '全部', cluster: '集群', node: '节点', service: '服务', pod: 'Pod',
  }

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '图谱视图' }]} />
      <PageHeader title="图谱视图" desc="集群资源知识图谱 · 图谱视图按类型着色、边按调用量缩放；列表视图按 集群→节点→服务 三级展开"
        actions={
          <Space>
            <Segmented value={view} onChange={(v) => setView(v as 'graph' | 'list')}
              options={[{ label: '图谱视图', value: 'graph' }, { label: '列表视图', value: 'list' }]} />
            <Select value={currentClusterId} onChange={(v) => setCurrentCluster(v)} style={{ width: 180 }}
              options={clusterOptions} placeholder="选择集群" />
          </Space>
        } />

      <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
        <Spin spinning={loading}>
          {unavailable ? (
            <Empty text="图谱构建中" hint="后端图谱服务尚未就绪（P2-1/P2-2 并行开发中），稍后自动可用" />
          ) : nodeCount === 0 ? (
            <Empty text="暂无图谱数据" hint="切换集群或等待图谱采集完成" />
          ) : view === 'graph' ? (
            <>
              {/* 图谱视图工具条：显示全部节点开关 */}
              <div style={{
                padding: '10px 16px', borderBottom: '1px solid var(--border-soft)',
                display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8,
              }}>
                <Space size={10}>
                  <span style={{ fontSize: 13, color: 'var(--text-secondary)' }}>显示全部节点</span>
                  <Switch size="small" checked={showAll} onChange={setShowAll} />
                  <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                    {showAll ? '含基础设施（node / pod / ...）' : '仅服务依赖拓扑'}
                  </span>
                </Space>
                <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>拖拽移动 · 滚轮缩放 · 悬停查看详情</span>
              </div>
              <div ref={chartRef} style={{ width: '100%', height: 'calc(100vh - 320px)', minHeight: 440 }} />
            </>
          ) : (
            <>
              {/* 列表视图工具条：类型筛选 + 名称搜索 */}
              <div style={{
                padding: '10px 16px', borderBottom: '1px solid var(--border-soft)',
                display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8,
              }}>
                <Space size={10}>
                  <Segmented value={typeFilter} onChange={(v) => setTypeFilter(v as typeof typeFilter)}
                    options={[
                      { label: '全部', value: 'all' },
                      { label: '集群', value: 'cluster' },
                      { label: '节点', value: 'node' },
                      { label: '服务', value: 'service' },
                      { label: 'Pod', value: 'pod' },
                    ]} />
                  <Input allowClear placeholder="按名称搜索" value={search}
                    onChange={(e) => setSearch(e.target.value)} style={{ width: 200 }} />
                </Space>
                <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                  共 {displayRows.length} 项{search || !isTree ? ` · ${typeFilterLabel[typeFilter]}视图` : ' · 树形视图'}
                </span>
              </div>
              <Table rowKey="key" size="middle" columns={columns} dataSource={displayRows}
                pagination={false} scroll={{ x: 760, y: 'calc(100vh - 380px)' }}
                expandable={isTree ? { defaultExpandedRowKeys: treeData.tree.map((r) => r.key) } : undefined}
                locale={{ emptyText: <Empty text={search ? '没有匹配的节点' : '暂无图谱数据'} hint="切换类型筛选或等待图谱采集完成" /> }} />
            </>
          )}
        </Spin>
        {nodeCount > 0 && (
          <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-soft)', fontSize: 12, color: 'var(--text-muted)', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
            <span>{stats.clusters} 集群 · {stats.nodes} 节点 · {stats.services} 服务 · {stats.pods} Pod · {stats.edges} 关系</span>
            {view === 'graph' && (
              <Space size={4}>
                {Object.entries(TYPE_COLORS).filter(([k]) => k !== 'other').map(([k, c]) => (
                  <span key={k} style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
                    <span style={{ width: 8, height: 8, borderRadius: '50%', background: c, display: 'inline-block' }} />
                    <span style={{ fontSize: 11 }}>{k}</span>
                  </span>
                ))}
              </Space>
            )}
            {view === 'graph' && (
              <span style={{ fontSize: 11 }}>
                <Tag style={{ marginRight: 4 }}>— 实线：调用/依赖</Tag>
                <Tag style={{ marginRight: 0 }}>┄ 虚线：归属/其他</Tag>
              </span>
            )}
          </div>
        )}
        {nodeCount === 0 && (
          <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-soft)', fontSize: 12, color: 'var(--text-muted)' }}>
            图谱数据将在此展示（后端 API 就绪后自动填充）
          </div>
        )}
      </div>
    </div>
  )
}

export default KnowledgeGraph
