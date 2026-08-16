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
const CATEGORIES = Object.entries(TYPE_COLORS).map(([name, color]) => ({ name, itemStyle: { color } }))

// 实线边类型（调用/依赖/包含等强关系用实线，其余用虚线区分）
const SOLID_EDGE_TYPES = ['calls', 'call', 'depends', 'dependency', 'belongs', 'contains', 'contain',
  'runs', 'run', 'deployed_on', 'deploy', 'use', 'uses', 'manage', 'connected']

function edgeType(e: KgEdge): 'solid' | 'dashed' {
  const t = String(e?.type || '').toLowerCase()
  if (!t) return 'solid'
  return SOLID_EDGE_TYPES.includes(t) ? 'solid' : 'dashed'
}

const KnowledgeGraph: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const setCurrentCluster = useUIStore((s) => s.setCurrentCluster)
  const clusters = useUIStore((s) => s.clusters)
  const refreshClusters = useUIStore((s) => s.refreshClusters)

  const chartRef = useRef<HTMLDivElement>(null)
  const chartInst = useRef<echarts.ECharts | null>(null)
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
    getKgGraph(currentClusterId !== 'all' ? { cluster_id: currentClusterId } : {})
      .then((r) => setGraph(r.data || { nodes: [], edges: [] }))
      .catch(() => setGraph({ nodes: [], edges: [], unavailable: true }))
      .finally(() => { if (!silent) setLoading(false) })
  }
  useEffect(() => { loadGraph(false) }, [currentClusterId])

  // 卸载时销毁 ECharts 实例
  useEffect(() => () => {
    if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
    chartInst.current = null
  }, [])

  // 渲染 force graph（options + resize，参照 ServiceObservability 写法）
  useEffect(() => {
    if (!chartRef.current) return
    if (!chartInst.current || chartInst.current.isDisposed() || chartInst.current.getDom() !== chartRef.current) {
      if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
      chartInst.current = echarts.init(chartRef.current)
    }
    const inst = chartInst.current

    const rawNodes: KgNode[] = graph?.nodes || []
    const rawEdges: KgEdge[] = graph?.edges || graph?.links || []
    const nodes = rawNodes.map((n) => {
      const type = String(n.type || 'other').toLowerCase()
      return {
        id: String(n.id ?? n.name),
        name: n.name || String(n.id),
        type,
        category: TYPE_COLORS[type] !== undefined ? type : 'other',
        symbolSize: type === 'service' ? 34 : type === 'switch' ? 30 : 26,
      }
    })
    const links = rawEdges.map((e) => ({
      source: String(e.source),
      target: String(e.target),
      type: e.type || 'calls',
      value: e.value,
      lineStyle: {
        type: edgeType(e),
        opacity: 0.65,
      },
    }))

    try {
      inst.setOption({
        tooltip: {
          trigger: 'item',
          formatter: (p: any) => {
            if (p.dataType === 'edge') {
              return `${p.data.source} → ${p.data.target}<br/><span style="color:#999">${p.data.type || '关系'}</span>`
            }
            const d = p.data
            return `<b>${d.name}</b><br/><span style="color:#999">${d.type || 'unknown'}</span>`
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
          layout: 'force',
          roam: true,
          draggable: true,
          data: nodes,
          links,
          categories: CATEGORIES,
          force: { repulsion: 200, edgeLength: 130, gravity: 0.1 },
          label: {
            show: true, position: 'bottom', fontSize: 10, color: '#1f2d3d',
            distance: 6, overflow: 'truncate', width: 90, showAbove: true,
          },
          lineStyle: { color: '#c6cfdb', width: 1.2, curveness: 0.15 },
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: [0, 7],
          emphasis: { focus: 'adjacency', lineStyle: { width: 3 }, label: { fontWeight: 600 } },
        }],
      })
    } catch (err) {
      console.error('[KnowledgeGraph] setOption 失败:', err)
    }

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
      <PageHeader title="图谱视图" desc="集群资源知识图谱 · 节点按类型着色，边按关系类型区分实线/虚线（构建中）"
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
            {nodeCount} 节点 · {edgeCount} 关系 · 拖拽可移动节点，滚轮缩放
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
