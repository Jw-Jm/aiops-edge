import React, { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Drawer, Spin, Button, Space, Tag, Statistic, Row, Col, Table, Segmented, Card, Tabs, Select, Badge, Typography, Empty as AntdEmpty } from 'antd'
import * as echarts from 'echarts'
import { getTopology, getServices, getTopologyNodeDetail, getServiceDetail } from '../../api/client'
import { PageHeader, Breadcrumb, StatusBadge, Empty } from '../../components/ui/PageKit'
import { computeHealth, healthColor } from '../../lib/health'

const { Text } = Typography

interface TopoNode { id: string; name: string; category: number; symbolSize?: number; _seed?: number; _count?: number }
interface TopoLink { source: string; target: string; value?: number }
interface ServiceRow { service: string; calls?: number; errors?: number; error_rate?: number; avg_latency_ms?: number }

// 节点"人话详情"：状态一句话 + 上下游数量与列表（对齐重构前 v1.1 设计）
function describeNode(
  name: string,
  metrics: any,
  links: TopoLink[],
): { status: string; sentence: string; up: string[]; down: string[] } {
  const up = new Set<string>()
  const down = new Set<string>()
  for (const l of links) {
    if (l.target === name) up.add(l.source)
    if (l.source === name) down.add(l.target)
  }
  const errorRate = metrics?.error_rate ?? metrics?.errorRate ?? 0
  const latency = metrics?.latency_ms ?? metrics?.avg_ms ?? 0
  const upList = Array.from(up).sort()
  const downList = Array.from(down).sort()
  if (errorRate > 0) {
    return { status: '异常', sentence: `${name} 当前状态：异常；错误率 ${(errorRate * 100).toFixed(1)}%，平均响应 ${Number(latency).toFixed(0)}ms`, up: upList, down: downList }
  }
  if (metrics && (metrics.calls || metrics.calls === 0)) {
    return { status: '正常', sentence: `${name} 当前状态：正常；平均响应 ${Number(latency).toFixed(0)}ms`, up: upList, down: downList }
  }
  return { status: '未知', sentence: `${name} 暂无实时指标`, up: upList, down: downList }
}

const METRIC_TYPES = [
  { value: 'errorRate', label: '请求错误率', key: (t: any) => t?.error_rate ?? t?.errorRate ?? 0 },
  { value: 'latency', label: '响应时间', key: (t: any) => t?.avg_ms ?? t?.latency_ms ?? 0 },
  { value: 'calls', label: '调用量', key: (t: any) => t?.calls ?? 0 },
  { value: 'errors', label: '错误数', key: (t: any) => t?.errors ?? 0 },
]

const ServiceObservability: React.FC = () => {
  const chartRef = useRef<HTMLDivElement>(null)
  const chartInst = useRef<echarts.ECharts | null>(null)
  const trendChartRef = useRef<HTMLDivElement>(null)
  const trendInst = useRef<echarts.ECharts | null>(null)
  const [view, setView] = useState<'topo' | 'list'>('topo')
  const [searchParams] = useSearchParams()
  const [loading, setLoading] = useState(true)
  const [nodes, setNodes] = useState<TopoNode[]>([])
  const [links, setLinks] = useState<TopoLink[]>([])
  const [services, setServices] = useState<ServiceRow[]>([])
  const [selectedNode, setSelectedNode] = useState<TopoNode | null>(null)
  const [nodeDetail, setNodeDetail] = useState<any>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [metricType, setMetricType] = useState('errorRate')

  // Issue5: 拓扑/服务数据加载；抽成函数以便定时刷新（默认 30s 轮询，接近实时）
  const loadData = (silent = false) => {
    if (!silent) setLoading(true)
    Promise.all([getTopology({ minutes: 60 }), getServices()])
      .then(([t, s]) => {
        const td = t.data
        const rawNodes: any[] = (td.nodes || []).map((n: any, i: number) => ({
          id: String(n.id ?? n.name ?? i), name: n.name || n.id, category: n.category ?? 0,
          symbolSize: 30 + ((n.metrics?.calls || 0) % 40),
        }))
        // 预置环形初始坐标，保证力导向布局在画布范围内（节点不超出页面）
        const N = Math.max(1, rawNodes.length)
        const nodesData: TopoNode[] = rawNodes.map((n, i) => ({
          ...n,
          _seed: i,
          _count: N,
        }))
        const linksData: TopoLink[] = (td.edges || td.links || []).map((e: any) => ({
          source: String(e.source_service ?? e.source ?? e.src),
          target: String(e.target_service ?? e.target ?? e.dst),
          value: e.calls ?? e.value ?? 1,
        })).filter((l: TopoLink) => l.source && l.target)
        setNodes(nodesData)
        setLinks(linksData)
        const sd = s.data
        // Issue6: 后端 /services 返回字段为 service_name/traces/spans/avg_ms/max_ms，
        // 前端需映射为 service/calls/avg_latency_ms 等表格列字段，否则服务名为空、调用量为0。
        const rawSvc = (Array.isArray(sd) ? sd : sd?.data || sd?.services || []) as any[]
        setServices(rawSvc.map((x: any) => ({
          ...x,
          service: x.service_name ?? x.service,
          calls: Number(x.traces ?? x.calls ?? 0),
          errors: Number(x.errors ?? 0),
          error_rate: Number(x.error_rate ?? 0),
          avg_latency_ms: Number(x.avg_ms ?? x.avg_latency_ms ?? 0),
        })))
      })
      .catch(() => { if (!silent) { setNodes([]); setLinks([]); setServices([]) } })
      .finally(() => { if (!silent) setLoading(false) })
  }

  useEffect(() => {
    loadData(false)
    // Issue5: 30s 静默轮询，保持拓扑/服务列表近实时；切换集群时也会因 uiStore 变化重新加载
    const timer = setInterval(() => loadData(true), 30000)
    return () => clearInterval(timer)
  }, [])

  // 支持 ?node=xxx 自动打开节点详情（深链分享 / 测试验证）
  useEffect(() => {
    const nodeName = searchParams.get('node')
    if (!nodeName || loading || nodes.length === 0) return
    // 避免重复打开：节点名相同且 drawer 已开则跳过
    if (drawerOpen && selectedNode?.name === nodeName) return
    const found = nodes.find((n) => n.name === nodeName)
    if (found || nodeName) openDetail(nodeName)
  }, [loading, nodes, searchParams, drawerOpen, selectedNode])

  useEffect(() => {
    // Issue1/2 根因修复：chartRef 容器在视图切换时会被卸载重建。必须确保：
    // (a) 离开 topo 视图时 dispose 实例并清空引用；
    // (b) 回到 topo 时若实例为 null/已 dispose/绑定的是旧容器(脱离 DOM)，重新 init。
    // 否则复用绑定在已卸载 div 上的旧实例，setOption 渲染到脱离 DOM 的 canvas 上 → 拓扑空白/白屏。
    if (view !== 'topo') {
      if (chartInst.current) {
        try { chartInst.current.dispose() } catch { /* ignore */ }
        chartInst.current = null
      }
      return
    }
    if (!chartRef.current) return
    if (!chartInst.current || chartInst.current.isDisposed() || chartInst.current.getDom() !== chartRef.current) {
      if (chartInst.current) { try { chartInst.current.dispose() } catch { /* ignore */ } }
      chartInst.current = echarts.init(chartRef.current)
    }
    const inst = chartInst.current
    const nodeScale = Math.max(1, nodes.length / 8) // 节点多则加大斥力
    // Issue3: 边宽按调用量缩放，但整体调细（0.7 + 1.3 缩放，最大约 2），避免太粗
    const maxCalls = Math.max(1, ...links.map((l: any) => Number(l.value) || 1))
    const linksWithWidth = links.map((l: any) => ({
      ...l,
      lineStyle: { width: 0.7 + 1.3 * (Number(l.value) || 0) / maxCalls },
    }))
    // 环形初始坐标（像素，基于容器尺寸），让节点在画布范围内开始受力，避免飞出页面
    const cw = chartRef.current.clientWidth || 800
    const ch = chartRef.current.clientHeight || 500
    const cx = cw / 2, cy = ch / 2, R = Math.min(cw, ch) * 0.34
    const N = Math.max(1, nodes.length)
    const dataWithPos = nodes.map((n, i) => ({
      ...n,
      x: cx + R * Math.cos((2 * Math.PI * i) / N),
      y: cy + R * Math.sin((2 * Math.PI * i) / N),
    }))
    try {
    inst.setOption({
      tooltip: {
        trigger: 'item',
        formatter: (p: any) => {
          if (p.dataType === 'edge') return `${p.data.source} → ${p.data.target}<br/>调用 ${p.data.value || 0} 次`
          return `<b>${p.data.name}</b>`
        },
      },
      series: [{
        type: 'graph', layout: 'force', roam: true, draggable: true,
        // 2.1 力导向边界约束：重力拉向中心 + 适中斥力，配合环形种子让节点保持在画布内
        force: {
          repulsion: 260 * nodeScale,
          edgeLength: [90, 180],
          gravity: 0.24,
          friction: 0.5,
        },
        data: dataWithPos,
        links: linksWithWidth,
        // 标签完整显示（不截断），位置在节点下方，字号稍小
        label: {
          show: true, position: 'bottom', fontSize: 10, color: '#1f2d3d',
          distance: 6,
          overflow: 'truncate', width: 90,
        },
        // Issue4: 边按调用量缩放宽度 + 末端箭头表达调用方向 + 增大曲率让双向调用分离
        lineStyle: {
          color: '#c6cfdb',
          width: 1.2,
          curveness: 0.22,
          opacity: 0.7,
        },
        edgeSymbol: ['none', 'arrow'],
        edgeSymbolSize: [0, 9],
        emphasis: {
          focus: 'adjacency',
          lineStyle: { width: 3 },
          label: { fontWeight: 600 },
        },
        itemStyle: { color: '#2f54eb', borderColor: '#fff', borderWidth: 1 },
      }],
    })
    } catch (err) {
      // Issue1: 任何 ECharts setOption 异常都不应冒泡导致 React 崩溃（白屏），静默降级
      console.error('[ServiceObservability] setOption 失败:', err)
    }
    // Issue3: 先 off 再 on，避免每次 nodes/links 变化重复注册 click 监听导致 handler 累积、
    // 点击一次触发多次 openDetail（抽屉闪开关）进而白屏。
    inst.off('click')
    inst.on('click', (p: any) => { if (p.dataType === 'node' && p.data?.name) openDetail(p.data.name) })
    const onResize = () => { try { inst.resize() } catch { /* ignore */ } }
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      // 实例生命周期已在 effect 顶部（view!=='topo' 分支）处理 dispose；此处仅清理事件
    }
  }, [view, nodes, links])

  const openDetail = (name: string) => {
    setSelectedNode(nodes.find((n) => n.name === name) || { id: name, name, category: 0 })
    setDrawerOpen(true)
    setDrawerLoading(true)
    setNodeDetail(null)
    getTopologyNodeDetail(name).then((r) => setNodeDetail(r.data)).catch(() => setNodeDetail({})).finally(() => setDrawerLoading(false))
  }

  const openServiceDetail = (name: string) => {
    setDrawerOpen(true)
    setDrawerLoading(true)
    setNodeDetail(null)
    getServiceDetail(name).then((r) => setNodeDetail(r.data)).catch(() => setNodeDetail({})).finally(() => setDrawerLoading(false))
  }

  // 节点详情：指标趋势图（对齐 v1.1）
  useEffect(() => {
    // Issue3: 抽屉 destroyOnClose 会销毁趋势图容器；关闭抽屉时 dispose 旧实例，避免复用脱离 DOM 实例白屏
    if (!drawerOpen) {
      if (trendInst.current) {
        trendInst.current.dispose()
        trendInst.current = null
      }
      return
    }
    if (!drawerOpen || drawerLoading || !trendChartRef.current) return
    // Issue1: 抽屉 destroyOnClose 后趋势图容器重建；若实例绑定的是旧容器则重 init，避免渲染到脱离 DOM 的 canvas
    if (!trendInst.current || trendInst.current.isDisposed() || trendInst.current.getDom() !== trendChartRef.current) {
      if (trendInst.current) { try { trendInst.current.dispose() } catch { /* ignore */ } }
      trendInst.current = echarts.init(trendChartRef.current)
    }
    const mt = METRIC_TYPES.find((m) => m.value === metricType) || METRIC_TYPES[0]
    const trend = nodeDetail?.trend || nodeDetail?.metrics?.trend || []
    const x = trend.map((t: any) => t?.t || t?.time || '')
    const data = trend.map((t: any) => Number(mt.key(t) || 0))
    const inst = trendInst.current
    try {
    inst.setOption({
      tooltip: { trigger: 'axis' },
      grid: { left: 44, right: 16, top: 20, bottom: 28 },
      xAxis: { type: 'category', data: x, axisLabel: { color: '#7a8794', fontSize: 10 } },
      yAxis: { type: 'value', axisLabel: { color: '#7a8794', fontSize: 10 }, splitLine: { lineStyle: { color: '#eef2f7' } } },
      series: [{ name: mt.label, type: 'line', smooth: true, symbol: 'none', data, itemStyle: { color: '#2f54eb' }, areaStyle: { opacity: 0.08 } }],
    })
    } catch (err) {
      console.error('[ServiceObservability] 趋势图 setOption 失败:', err)
    }
    const onResize = () => { try { inst.resize() } catch { /* ignore */ } }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [drawerOpen, drawerLoading, nodeDetail, metricType])

  // 节点详情：最近调用链列
  const traceColumns = [
    { title: 'Trace ID', dataIndex: 'trace_id', key: 'trace_id', render: (v: string) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11 }}>{(v || '').slice(0, 16)}</span> },
    { title: '状态', dataIndex: 'is_error', key: 'is_error', width: 70, render: (v: number) => v ? <StatusBadge text="错误" tone="crit" /> : <StatusBadge text="正常" tone="ok" /> },
    { title: '延迟', dataIndex: 'duration_ms', key: 'duration_ms', render: (v: number, r: any) => `${(r?.max_ms ?? v ?? 0).toFixed(1)}ms` },
  ]
  const spanColumns = [
    { title: '操作', dataIndex: 'operation_name', key: 'operation_name' },
    { title: '服务', dataIndex: 'service_name', key: 'service_name' },
    { title: '状态', dataIndex: 'is_error', key: 'is_error', width: 70, render: (v: number) => v ? <StatusBadge text="错误" tone="crit" /> : <StatusBadge text="正常" tone="ok" /> },
    { title: '延迟', dataIndex: 'ms', key: 'ms', render: (v: number) => `${Number(v || 0).toFixed(2)}ms` },
  ]

  const columns = [
    { title: '服务', dataIndex: 'service', key: 'service', render: (v: string) => <a onClick={() => openServiceDetail(v)} style={{ color: 'var(--primary)' }}>{v}</a> },
    { title: '调用量', dataIndex: 'calls', key: 'calls', render: (v: number) => (v ?? 0).toLocaleString() },
    { title: '错误数', dataIndex: 'errors', key: 'errors', render: (v: number, r: any) => r.errors > 0 ? <StatusBadge text={`${r.errors}`} tone="crit" /> : <span style={{ color: 'var(--text-muted)' }}>0</span> },
    { title: '错误率', dataIndex: 'error_rate', key: 'error_rate', render: (v: number) => `${((v ?? 0) * 100).toFixed(2)}%` },
    { title: '平均延迟', dataIndex: 'avg_latency_ms', key: 'avg_latency_ms', render: (v: number) => `${(v ?? 0).toFixed(2)}ms` },
  ]

  const nodeName = selectedNode?.name || nodeDetail?.service_name || nodeDetail?.name || ''
  const desc = describeNode(nodeName, nodeDetail?.metrics || nodeDetail, links)
  const nd = nodeDetail || {}
  // 2.4 健康度：基于详情返回的 Apdex 与错误率统一计算
  const health = computeHealth({
    apdex: nd?.apdex ?? nd?.metrics?.apdex ?? null,
    errorRate: nd?.error_rate ?? nd?.metrics?.error_rate ?? null,
  })
  // 2.3 调用次数：从边数据里按 source/target 关联统计（须在 upData/downData 之前声明，
  // 否则 map 回调引用 const 触发 TDZ：Cannot access 'X' before initialization → 白屏）
  const edgeCountFor = (name: string, dir: 'out' | 'in') => {
    let total = 0
    for (const l of links) {
      const v = Number((l as any).value || 0)
      if (dir === 'out' && l.source === name) total += v
      if (dir === 'in' && l.target === name) total += v
    }
    return total
  }
  const upData = desc.up.map((n, i) => ({ key: `${n}-${i}`, name: n, count: edgeCountFor(n, 'in') }))
  const downData = desc.down.map((n, i) => ({ key: `${n}-${i}`, name: n, count: edgeCountFor(n, 'out') }))
  const relCols = [
    { title: '服务', dataIndex: 'name', key: 'name', render: (v: string) => <a onClick={() => openServiceDetail(v)} style={{ color: 'var(--primary)' }}>{v}</a> },
    { title: '调用次数', dataIndex: 'count', key: 'count', width: 90, render: (v: number) => (v ?? 0).toLocaleString() },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '可观测' }, { t: '服务全景' }]} />
      <PageHeader title="服务全景" desc="服务调用关系与健康一览 · 点击节点/服务查看详情"
        actions={<Segmented value={view} onChange={(v) => setView(v as any)} options={[{ label: '拓扑视图', value: 'topo' }, { label: '服务列表', value: 'list' }]} />} />

      {view === 'topo' ? (
        <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
          <Spin spinning={loading}>
            <div ref={chartRef} style={{ width: '100%', height: 'calc(100vh - 220px)', minHeight: 480 }} />
            {nodes.length === 0 && !loading && <Empty text="暂无拓扑数据" />}
          </Spin>
          <div style={{ padding: '8px 16px', borderTop: '1px solid var(--border-soft)', fontSize: 12, color: 'var(--text-muted)' }}>
            {nodes.length} 节点 · {links.length} 关系 · 拖拽可移动节点，点击节点查看详情
          </div>
        </div>
      ) : (
        <div className="card" style={{ padding: 0 }}>
          <Table rowKey="service" loading={loading} columns={columns} dataSource={services}
            pagination={{ pageSize: 15 }} size="middle" scroll={{ x: 700 }}
            locale={{
              emptyText: (
                <AntdEmpty description="服务目录为空，可前往拓扑图查看">
                  <Button type="primary" onClick={() => setView('topo')}>查看拓扑图</Button>
                </AntdEmpty>
              ),
            }} />
        </div>
      )}

      <Drawer width={620} open={drawerOpen} onClose={() => setDrawerOpen(false)} destroyOnClose
        title={<Space><Text style={{ fontSize: 15, color: 'var(--text)', fontWeight: 600 }}>{nodeName}</Text></Space>}
        styles={{ body: { padding: 16, background: 'var(--surface-1)' } }}>
        <Spin spinning={drawerLoading}>
          {desc.status !== '未知' && (
            <div style={{ marginBottom: 16, padding: '14px 16px', borderRadius: 8, background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
              <Space style={{ marginBottom: 6 }}>
                <Badge color={desc.status === '异常' ? 'var(--danger)' : 'var(--success)'}
                  text={<Text style={{ fontSize: 13, fontWeight: 600, color: desc.status === '异常' ? 'var(--danger)' : 'var(--success)' }}>{desc.status}</Text>} />
                <Text style={{ fontSize: 13, color: 'var(--text)' }}>{desc.sentence}</Text>
              </Space>
              <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
                调用 {desc.down.length} 个服务 · 被 {desc.up.length} 个服务调用
              </div>
            </div>
          )}

          {/* 2.4 健康度：Apdex×0.7 + (1-错误率)×0.3 综合分档 */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16, padding: '10px 16px', borderRadius: 8, background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
            <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--text)' }}>健康度</span>
            <span style={{ fontSize: 20, fontWeight: 700, color: healthColor(health.level) }}>
              {health.score !== null ? health.score.toFixed(2) : '--'}
            </span>
            <StatusBadge text={health.label} tone={health.level === 'critical' ? 'crit' : health.level === 'warning' ? 'warn' : health.level === 'healthy' ? 'ok' : 'muted'} />
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              依据 Apdex × 0.7 + (1 − 错误率) × 0.3 计算，≥0.9 健康 / 0.7-0.9 亚健康 / &lt;0.7 异常
            </span>
          </div>

          {(nd.apdex !== undefined || nd.latency_ms !== undefined || nd.error_rate !== undefined || nd.throughput !== undefined) && (
            <Row gutter={12} style={{ marginBottom: 16 }}>
              <Col span={6}><Card size="small" styles={{ body: { background: 'var(--surface-2)', border: '1px solid var(--border)', padding: '14px 16px' } }} style={{ height: '100%' }}><Statistic title="Apdex" value={nd.apdex ?? 0} precision={2} valueStyle={{ color: (nd.apdex ?? 0) >= 0.9 ? 'var(--success)' : (nd.apdex ?? 0) >= 0.7 ? 'var(--warning)' : 'var(--danger)' }} /></Card></Col>
              <Col span={6}><Card size="small" styles={{ body: { background: 'var(--surface-2)', border: '1px solid var(--border)', padding: '14px 16px' } }} style={{ height: '100%' }}><Statistic title="响应时间" value={nd.latency_ms ?? 0} suffix="ms" precision={2} valueStyle={{ color: (nd.latency_ms ?? 0) > 1000 ? 'var(--danger)' : (nd.latency_ms ?? 0) > 300 ? 'var(--warning)' : 'var(--primary)' }} /></Card></Col>
              <Col span={6}><Card size="small" styles={{ body: { background: 'var(--surface-2)', border: '1px solid var(--border)', padding: '14px 16px' } }} style={{ height: '100%' }}><Statistic title="请求错误率" value={nd.error_rate ?? 0} suffix="%" precision={2} valueStyle={{ color: (nd.error_rate ?? 0) > 3 ? 'var(--danger)' : 'var(--success)' }} /></Card></Col>
              <Col span={6}><Card size="small" styles={{ body: { background: 'var(--surface-2)', border: '1px solid var(--border)', padding: '14px 16px' } }} style={{ height: '100%' }}><Statistic title="吞吐率" value={nd.throughput ?? 0} suffix="rpm" valueStyle={{ color: 'var(--success)' }} /></Card></Col>
            </Row>
          )}

          <Card size="small" title="指标趋势" extra={<Select size="small" value={metricType} onChange={setMetricType} style={{ width: 130 }} options={METRIC_TYPES.map((m) => ({ value: m.value, label: m.label }))} />}
            styles={{ body: { background: 'var(--surface-2)' }, header: { background: 'var(--surface-2)', color: 'var(--text)' } }}
            style={{ marginBottom: 16, border: '1px solid var(--border)' }}>
            <Text style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              {nodeDetail?.metrics?.calls ? `共采集 ${nodeDetail.metrics.calls} 条调用，其中错误 ${nodeDetail.metrics.errors || 0} 条` : ''}
            </Text>
            <div ref={trendChartRef} style={{ height: 220, marginTop: 12 }} />
          </Card>

          <Card size="small" title="调用关系" styles={{ body: { background: 'var(--surface-2)' }, header: { background: 'var(--surface-2)', color: 'var(--text)' } }}
            style={{ marginBottom: 16, border: '1px solid var(--border)' }}>
            <Row gutter={16}>
              <Col span={12}>
                <Text strong style={{ fontSize: 13 }}>调用（下游 {desc.down.length}）</Text>
                {downData.length > 0
                  ? <Table size="small" columns={relCols} dataSource={downData} rowKey="key" pagination={false} style={{ marginTop: 8 }} />
                  : <div style={{ color: 'var(--text-muted)', fontSize: 12, padding: '8px 0' }}>无下游调用</div>}
              </Col>
              <Col span={12}>
                <Text strong style={{ fontSize: 13 }}>被调用（上游 {desc.up.length}）</Text>
                {upData.length > 0
                  ? <Table size="small" columns={relCols} dataSource={upData} rowKey="key" pagination={false} style={{ marginTop: 8 }} />
                  : <div style={{ color: 'var(--text-muted)', fontSize: 12, padding: '8px 0' }}>无上游调用</div>}
              </Col>
            </Row>
          </Card>

          <Card size="small" styles={{ body: { background: 'var(--surface-2)', padding: 0 }, header: { background: 'var(--surface-2)', color: 'var(--text)' } }} style={{ border: '1px solid var(--border)' }}>
            <Tabs defaultActiveKey="traces" items={[
              { key: 'traces', label: '调用链', children: <Table size="small" columns={traceColumns} dataSource={nodeDetail?.traces || []} rowKey="trace_id" pagination={{ pageSize: 5 }} /> },
              { key: 'spans', label: 'Span 明细', children: <Table size="small" columns={spanColumns} dataSource={nodeDetail?.spans || []} rowKey={(r: any) => `${r?.operation_name}-${r?.span_id || ''}-${r?.start_time || ''}`} pagination={{ pageSize: 5 }} /> },
            ]} />
          </Card>
        </Spin>
      </Drawer>
    </div>
  )
}

export default ServiceObservability
