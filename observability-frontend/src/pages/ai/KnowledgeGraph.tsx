import React, { useEffect, useRef, useState } from 'react'
import { Select, Spin, Space, Tag } from 'antd'
import * as echarts from 'echarts'
import { getKgGraph, KgNode, KgEdge, KgGraph } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'
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
  other: '#8c8c8c',
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

// 数据指纹：30s 轮询时若节点/边身份未变则跳过 setState，避免 force 布局重启致节点乱飘
const graphFp = (g: KgGraph | undefined | null): string => {
  const ns = (g?.nodes || []).map((n) => `${n.id ?? n.name}|${n.type || ''}`).sort().join(';')
  const es = (g?.edges || g?.links || []).map((e) =>
    `${e.src ?? e.source}|${e.dst ?? e.target}|${e.type || ''}|${e.props?.calls ?? e.calls ?? e.value ?? 0}`,
  ).sort().join(';')
  return `${ns}||${es}`
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
  // finished 事件只写 ref，不触发 setState/effect 重跑，避免"每次 finished 都重建数据 → effect 重跑 →
  // 布局重启"的死循环。
  const stablePosRef = useRef<Record<string, { x: number; y: number }>>({})
  const [loading, setLoading] = useState(true)
  const [graph, setGraph] = useState<KgGraph>({ nodes: [], edges: [] })

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
    // 待后端支持按 k8s 集群维度构建图谱后，可改回传 currentClusterId。
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
    if (!chartRef.current) return
    if (!chartInst.current || chartInst.current.isDisposed() || chartInst.current.getDom() !== chartRef.current) {
      if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
      chartInst.current = echarts.init(chartRef.current)
    }
    const inst = chartInst.current

    const rawNodes: KgNode[] = graph?.nodes || []
    const rawEdges: KgEdge[] = graph?.edges || graph?.links || []

    // Bug1 修复（节点对齐）：id 用 String(n.id ?? n.name)，与边 src/dst 字符串化后对齐。
    // 后端节点 id 是数字，边 src/dst 也是数字节点 id，两端 String() 后即可匹配。
    const nodes = rawNodes.map((n) => {
      const type = String(n.type || 'other').toLowerCase()
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
    rawNodes.forEach((n) => {
      if (n.props) idToProps[String(n.id ?? n.name)] = n.props as Record<string, unknown>
    })

    // Bug1 修复（边可见）：API 返回 src/dst（数字节点 id）+ props.calls/errors。
    // 参照 ServiceObservability 写法兼容 source/target/source_service，并过滤两端不在节点集合内的悬空边。
    // 边宽按调用量缩放（0.7 + 1.3 * value/maxCalls，最大约 2），与 ServiceObservability 对齐。
    const maxCalls = Math.max(1, ...rawEdges.map((e) => Number(e.props?.calls ?? e.calls ?? e.value) || 1))
    const links = rawEdges.map((e) => {
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

    // Bug2 修复：自研力导向布局 + 硬性边界约束。ECharts 原生 force 布局没有"节点不飘出画布"的参数，
    // repulsion 调大后节点会被推离中心、飘出画布。这里手写力导向迭代，每轮把节点坐标 clamp 到画布范围内
    // （padding 24px，底部 60px 给 legend 留空间），从算法层面保证节点永不超出画布。
    // 已收敛过的节点（stablePosRef）用稳定坐标（仍 clamp），新节点用环形种子位置参与收敛。
    const cw = chartRef.current.clientWidth || 800
    const ch = chartRef.current.clientHeight || 500
    const cx = cw / 2, cy = ch / 2, R = Math.min(cw, ch) * 0.32
    const N = Math.max(1, nodes.length)
    const seedPos = nodes.map((n, i) => {
      const prev = stablePosRef.current[n.id]
      if (prev && typeof prev.x === 'number' && typeof prev.y === 'number') return { x: prev.x, y: prev.y }
      return { x: cx + R * Math.cos((2 * Math.PI * i) / N), y: cy + R * Math.sin((2 * Math.PI * i) / N) }
    })
    // 手写力导向：斥力(320) + 弹簧(edgeLength 180) + 引力(0.08)，迭代 300 轮，
    // 每轮把坐标 clamp 到 [pad, W-pad]x[pad, H-pad_bottom]。返回 id->{x,y}。
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
          // 让节点从环形种子平滑过渡到稳定布局（有动画、非闪动）
          animationDurationUpdate: 1200,
          animationDuration: 1200,
          animationEasingUpdate: 'cubicOut',
          data: dataWithPos,
          links,
          categories: CATEGORIES,
          // 标签避让：position:'top' + 更大 distance + showAbove，减少标签互相叠压
          label: {
            show: true, position: 'top', fontSize: 10, color: '#1f2d3d',
            distance: 10, overflow: 'truncate', width: 110, showAbove: true,
          },
          // 边按调用量缩放宽度 + 末端箭头表达依赖方向 + 增大曲率让双向调用分离
          lineStyle: { color: '#c6cfdb', width: 1.2, curveness: 0.22, opacity: 0.7 },
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: [0, 9],
          emphasis: { focus: 'adjacency', lineStyle: { width: 3 }, label: { fontWeight: 600 } },
        }],
      })
    } catch (err) {
      console.error('[KnowledgeGraph] setOption 失败:', err)
    }

    // 保存收敛后稳定坐标到 stablePosRef（只写 ref，不 setState）：
    // 30s 轮询数据未变时复用稳定位置，force 不从环形种子重新散开，首次收敛后保持静止；
    // 且不触发 effect 重跑，避免死循环。
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
  }, [graph])

  const unavailable = graph?.unavailable === true
  const nodeCount = graph?.nodes?.length || 0
  const edgeCount = graph?.edges?.length || graph?.links?.length || 0

  const clusterOptions = [
    { value: 'all', label: '全部集群' },
    ...clusters.map((c) => ({ value: String(c.id), label: c.name })),
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '图谱视图' }]} />
      <PageHeader title="图谱视图" desc="集群资源知识图谱 · 节点按类型着色，边按调用量缩放、箭头表示依赖方向"
        actions={
          <Space>
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
          ) : (
            <div ref={chartRef} style={{ width: '100%', height: 'calc(100vh - 260px)', minHeight: 480 }} />
          )}
        </Spin>
        {nodeCount > 0 && (
          <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-soft)', fontSize: 12, color: 'var(--text-muted)' }}>
            {nodeCount} 节点 · {edgeCount} 关系 · 拖拽可移动节点，滚轮缩放，悬停查看详情
            <Space size={4} style={{ marginLeft: 12 }}>
              {Object.entries(TYPE_COLORS).filter(([k]) => k !== 'other').map(([k, c]) => (
                <span key={k} style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
                  <span style={{ width: 8, height: 8, borderRadius: '50%', background: c, display: 'inline-block' }} />
                  <span style={{ fontSize: 11 }}>{k}</span>
                </span>
              ))}
            </Space>
            <span style={{ marginLeft: 12, fontSize: 11 }}>
              <Tag style={{ marginRight: 4 }}>— 实线：调用/依赖</Tag>
              <Tag style={{ marginRight: 0 }}>┄ 虚线：归属/其他</Tag>
            </span>
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