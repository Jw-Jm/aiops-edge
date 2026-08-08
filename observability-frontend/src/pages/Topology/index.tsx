import React, { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Card, Spin, Empty, Typography, Space, Select, Button, Tooltip,
  Drawer, Table, Tabs, Badge, Statistic, Row, Col, Checkbox, Switch, Alert, Collapse, message,
} from 'antd'
import {
  ReloadOutlined, DownloadOutlined, FullscreenOutlined,
} from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import * as echarts from 'echarts'
import html2canvas from 'html2canvas'
import { TopologyGraph, type NodeMetric, typeTierLabel } from '../../components/topology/TopologyGraph'
import {
  getTopology, getTopologyNodeDetail, getAlertEvents,
  topoListNodes, topoListRelations, topoListRelationTypes, topoSyncCatalog,
  type TopologyNodeItem, type TopologyRelationItem, type TopologyRelationTypeItem,
} from '../../api/client'
import { fmtLocalTime, fmtLocalHM } from '../../utils/date'

const { Text } = Typography

// 指标选择：趋势图展示的指标类型
const METRIC_OPTIONS: Record<string, {
  label: string; unit: string; yAxisName: [string, string?];
  series: Array<{ name: string; data: (t: any) => number; color: string; type?: 'bar' | 'line'; yAxisIndex?: number }>;
}> = {
  errorRate: {
    label: '请求错误率', unit: '%', yAxisName: ['错误率(%)', '调用量'],
    series: [
      { name: '请求错误率', data: (t) => Number(((t.errors || 0) / Math.max(1, t.calls || 1) * 100).toFixed(2)), color: '#ff6b6b', type: 'line', yAxisIndex: 0 },
      { name: '总调用量', data: (t) => Number(t.calls || 0), color: '#4e9bff', type: 'bar', yAxisIndex: 1 },
    ],
  },
  latency: {
    label: '响应时间', unit: 'ms', yAxisName: ['响应时间(ms)'],
    series: [
      { name: '平均响应时间', data: (t) => Number((t.avg_ms || 0).toFixed(2)), color: '#52c41a', type: 'line', yAxisIndex: 0 },
    ],
  },
  calls: {
    label: '调用量', unit: '次', yAxisName: ['调用量(次)'],
    series: [
      { name: '调用量', data: (t) => Number(t.calls || 0), color: '#4e9bff', type: 'bar', yAxisIndex: 0 },
      { name: '错误调用', data: (t) => Number(t.errors || 0), color: '#ff6b6b', type: 'bar', yAxisIndex: 0 },
    ],
  },
  errors: {
    label: '错误数', unit: '次', yAxisName: ['错误数(次)'],
    series: [
      { name: '错误数', data: (t) => Number(t.errors || 0), color: '#ff6b6b', type: 'bar', yAxisIndex: 0 },
    ],
  },
}

const fmtBig = (n: number): string => {
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(2) + 'K'
  return String(Math.round(n))
}

// 构建"人话摘要"：合并 trace 异常节点 + 告警事件。
// 返回 { type: 'error'|'success', message }，全部中文，一句话。
function buildSummary(
  alerts: any[],
  metrics: Record<string, NodeMetric>,
  relations: TopologyRelationItem[],
  nodes: TopologyNodeItem[],
): { type: 'error' | 'success'; message: string } {
  // 1. trace 异常节点（error_rate > 0）
  const abnormal = nodes.filter((n) => metrics[n.name]?.error_rate > 0)
  if (abnormal.length > 0) {
    const worst = abnormal.sort((a, b) => metrics[b.name].error_rate - metrics[a.name].error_rate)[0]
    const affected = new Set<number>()
    for (const r of relations) {
      if (r.src_id === worst.id || r.dst_id === worst.id) affected.add(r.src_id === worst.id ? r.dst_id : r.src_id)
    }
    const err = metrics[worst.name].error_rate.toFixed(1)
    return {
      type: 'error',
      message: `⚠️ 检测到 ${worst.name} 错误率升高（${err}%），可能影响 ${affected.size} 个相关服务`,
    }
  }
  // 2. 告警事件
  const firing = alerts.filter((a) => a.status === 'firing' || a.incident_id)
  if (firing.length > 0) {
    const top = firing[0]
    return { type: 'error', message: `⚠️ 存在 ${firing.length} 条未处理告警：${top.rule_name || top.service || '未知服务'}` }
  }
  // 3. 正常
  return { type: 'success', message: `✅ 系统运行正常（${nodes.length} 个服务正在监控）` }
}

// 节点"人话详情"：状态一句话 + 上下游数量。
function describeNode(
  node: TopologyNodeItem,
  metrics: Record<string, NodeMetric>,
  relations: TopologyRelationItem[],
  nodes: TopologyNodeItem[],
): { status: string; sentence: string; upCount: number; downCount: number } {
  const m = metrics[node.name]
  const nameById = new Map(nodes.map((n) => [n.id, n.name]))
  const up = new Set<number>()
  const down = new Set<number>()
  for (const r of relations) {
    if (r.dst_id === node.id) up.add(r.src_id)
    if (r.src_id === node.id) down.add(r.dst_id)
  }
  if (m && m.error_rate > 0) {
    return {
      status: '异常',
      sentence: `${node.name} 当前状态：异常；错误率 ${m.error_rate.toFixed(1)}%，平均响应 ${m.latency_ms?.toFixed?.(0) ?? 0}ms`,
      upCount: up.size,
      downCount: down.size,
    }
  }
  if (m) {
    return {
      status: '正常',
      sentence: `${node.name} 当前状态：正常；平均响应 ${m.latency_ms?.toFixed?.(0) ?? 0}ms`,
      upCount: up.size,
      downCount: down.size,
    }
  }
  return { status: '未知', sentence: `${node.name} 暂无实时指标`, upCount: up.size, downCount: down.size }
}

const Topology: React.FC = () => {
  const navigate = useNavigate()
  const wrapRef = React.useRef<HTMLDivElement>(null)

  const [nodes, setNodes] = useState<TopologyNodeItem[]>([])
  const [relations, setRelations] = useState<TopologyRelationItem[]>([])
  const [relationTypes, setRelationTypes] = useState<TopologyRelationTypeItem[]>([])
  const [loading, setLoading] = useState(true)
  const [typeFilter, setTypeFilter] = useState<string>('')
  const [hideOrphans, setHideOrphans] = useState(false)
  const [onlyAbnormal, setOnlyAbnormal] = useState(false)
  const [visibleRelTypes, setVisibleRelTypes] = useState<Set<string> | null>(null)
  const [timeRange, setTimeRange] = useState<number>(60)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [selectedNode, setSelectedNode] = useState<TopologyNodeItem | null>(null)
  const [nodeDetail, setNodeDetail] = useState<any>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [metricType, setMetricType] = useState<'errorRate' | 'latency' | 'calls' | 'errors'>('errorRate')
  const [nodeMetrics, setNodeMetrics] = useState<Record<string, NodeMetric>>({})
  const [alerts, setAlerts] = useState<any[]>([])

  const fetchCatalog = async () => {
    const [nr, rr, rtr, gt, ar] = await Promise.all([
      topoListNodes(typeFilter ? { type: typeFilter, limit: 2000 } : { limit: 2000 }),
      topoListRelations({ limit: 5000 }),
      topoListRelationTypes(),
      getTopology({ minutes: timeRange }).catch(() => null),
      getAlertEvents({ limit: 10, status: 'firing' }).catch(() => null),
    ])
    setAlerts(ar?.data?.items || [])
    setNodes(nr.data?.items || [])
    setRelations(rr.data?.items || [])
    setRelationTypes(rtr.data?.items || [])
    // 合并 trace 实时指标（error_rate/latency_ms/health）到节点，按服务名
    const m: Record<string, NodeMetric> = {}
    const tn = gt?.data?.nodes || []
    for (const n of tn) {
      if (!n.name) continue
      m[n.name] = {
        error_rate: Number(n.error_rate ?? 0),
        latency_ms: Number(n.latency_ms ?? 0),
        health: n.health || (Number(n.error_rate ?? 0) > 0 ? 'error' : 'healthy'),
        health_score: Number(n.health_score ?? 100),
      }
    }
    setNodeMetrics(m)
  }

  const load = async (forceSync = false) => {
    setLoading(true)
    try {
      await fetchCatalog()
      // 目录为空时（或强制同步时）从 trace 聚合自动填充
      if (forceSync || nodes.length === 0) {
        await topoSyncCatalog()
        await fetchCatalog()
      }
    } catch (e) {
      message.error('加载拓扑失败')
      setNodes([])
      setRelations([])
      setRelationTypes([])
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [typeFilter])
  useEffect(() => {
    if (visibleRelTypes === null && relationTypes.length > 0) {
      setVisibleRelTypes(new Set(relationTypes.map((rt) => rt.name)))
    }
  }, [relationTypes, visibleRelTypes])

  const openNodeDetail = async (node: TopologyNodeItem) => {
    setSelectedNode(node)
    setDrawerOpen(true)
    setDetailLoading(true)
    try {
      const res = await getTopologyNodeDetail(node.name)
      setNodeDetail(res.data?.data || res.data)
    } catch (e) {
      setNodeDetail(null)
    } finally {
      setDetailLoading(false)
    }
  }

  const handleFullscreen = () => {
    const el = wrapRef.current
    if (!el) return
    if (document.fullscreenElement) document.exitFullscreen()
    else el.requestFullscreen?.()
  }

  const handleDownload = async () => {
    const target = wrapRef.current
    if (!target) return
    try {
      const canvas = await html2canvas(target, { backgroundColor: '#0b1220', scale: 2, useCORS: true, logging: false })
      const url = canvas.toDataURL('image/png')
      const a = document.createElement('a')
      a.href = url
      a.download = `topology-${Date.now()}.png`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch (e) {
      console.error('[topology] download failed:', e)
    }
  }

  // 类型 chip：按目录节点类型聚合数量
  const typeChips = useMemo(() => {
    const counts = new Map<string, number>()
    nodes.forEach((n) => counts.set(n.type, (counts.get(n.type) || 0) + 1))
    return [...counts.entries()].sort((a, b) => b[1] - a[1])
  }, [nodes])

  // 顶部自动摘要（人话）
  const summary = useMemo(
    () => buildSummary(alerts, nodeMetrics, relations, nodes),
    [alerts, nodeMetrics, relations, nodes],
  )

  // 异常节点名集合（error_rate > 0）
  const abnormalNames = useMemo(() => {
    const set = new Set<string>()
    for (const n of nodes) {
      const m = nodeMetrics[n.name]
      if (m && m.error_rate > 0) set.add(n.name)
    }
    return set
  }, [nodes, nodeMetrics])

  // 默认聚焦：错误率最高的异常节点（无异常则不聚焦）
  const [focusNodeId, setFocusNodeId] = useState<number | null>(null)
  const focusTarget = useMemo(() => {
    if (abnormalNames.size === 0) return null
    let worst: TopologyNodeItem | null = null
    for (const n of nodes) {
      if (!abnormalNames.has(n.name)) continue
      if (!worst || (nodeMetrics[n.name].error_rate > nodeMetrics[worst.name].error_rate)) worst = n
    }
    return worst
  }, [nodes, abnormalNames, nodeMetrics])
  useEffect(() => {
    setFocusNodeId(focusTarget ? focusTarget.id : null)
  }, [focusTarget])

  const trendOption = useMemo(() => {
    if (!nodeDetail?.trend?.length) return {}
    const x = nodeDetail.trend.map((t: any) => {
      const v = t.t || t.time
      return fmtLocalHM(typeof v === 'string' ? v : v == null ? '' : String(v))
    })
    const cfg = METRIC_OPTIONS[metricType] || METRIC_OPTIONS.errorRate
    const dual = cfg.series.some((s) => s.yAxisIndex === 1)
    const series = cfg.series.map((s) => {
      const isLine = s.type === 'line'
      const isRight = s.yAxisIndex === 1
      const base: any = {
        name: s.name,
        type: s.type || 'bar',
        data: nodeDetail.trend.map(s.data),
        yAxisIndex: s.yAxisIndex || 0,
        itemStyle: { color: s.color, borderRadius: isLine ? undefined : (isRight ? [4, 4, 0, 0] : [2, 2, 0, 0]) },
        barMaxWidth: dual ? (isRight ? 14 : 10) : 24,
      }
      if (isLine) {
        base.smooth = true
        base.symbol = 'circle'
        base.symbolSize = 4
        base.lineStyle = { color: s.color, width: 2 }
        base.itemStyle = { color: s.color }
        base.areaStyle = {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: s.color + '55' },
            { offset: 1, color: s.color + '05' },
          ]),
          opacity: 1,
        }
        base.showSymbol = true
      } else if (isRight) {
        base.itemStyle = { color: s.color, opacity: 0.55, borderRadius: [4, 4, 0, 0] }
      }
      return base
    })
    const axisText = { color: 'rgba(255,255,255,0.55)', fontSize: 10 }
    const axisBase = {
      type: 'value' as const,
      axisLabel: axisText,
      splitLine: { lineStyle: { color: 'rgba(255,255,255,0.06)' } },
      axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } },
    }
    const yAxis = dual
      ? [
          { ...axisBase, name: cfg.yAxisName[0], nameTextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 10 }, axisLabel: { ...axisText, formatter: (v: number) => fmtBig(v) + '%' }, splitLine: { show: true, lineStyle: { color: 'rgba(255,255,255,0.06)' } } },
          { ...axisBase, name: cfg.yAxisName[1], nameTextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 10 }, axisLabel: { ...axisText, formatter: (v: number) => fmtBig(v) }, splitLine: { show: false } },
        ]
      : { ...axisBase, name: cfg.yAxisName[0], nameTextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 10 }, axisLabel: { ...axisText, formatter: (v: number) => fmtBig(v) + (cfg.unit === '%' ? '%' : '') } }
    const formatter = (params: any) => {
      const arr = Array.isArray(params) ? params : [params]
      const header = `<div style="font-weight:600;margin-bottom:4px;color:#fff">${arr[0]?.axisValue || ''}</div>`
      const rows = arr.map((p: any) => {
        let val: string
        if (p.seriesName === '请求错误率') val = Number(p.value).toFixed(2) + '%'
        else if (p.seriesName === '平均响应时间') val = Number(p.value).toFixed(2) + ' ms'
        else val = fmtBig(Number(p.value))
        return `<div style="display:flex;align-items:center;gap:6px;line-height:1.7">
          <span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:${p.color}"></span>
          <span style="color:rgba(255,255,255,0.75)">${p.seriesName}</span>
          <span style="margin-left:auto;padding-left:16px;font-weight:600;color:#fff">${val}</span>
        </div>`
      })
      return header + rows.join('')
    }
    return {
      backgroundColor: 'transparent',
      grid: dual ? { top: 34, right: 52, bottom: 26, left: 50, containLabel: true } : { top: 30, right: 24, bottom: 26, left: 50, containLabel: true },
      tooltip: { trigger: 'axis', backgroundColor: 'rgba(22,30,46,0.95)', borderColor: 'rgba(255,255,255,0.12)', borderWidth: 1, textStyle: { color: '#fff', fontSize: 11 }, padding: [10, 12], formatter },
      legend: { data: cfg.series.map((s) => s.name), textStyle: { color: 'rgba(255,255,255,0.6)', fontSize: 11 }, bottom: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 8, itemGap: 16 },
      xAxis: { type: 'category', data: x, axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } }, axisLabel: { color: 'rgba(255,255,255,0.5)', fontSize: 10 }, axisTick: { show: false } },
      yAxis,
      series,
    }
  }, [nodeDetail, metricType])

  const traceColumns = [
    { title: 'Trace ID', dataIndex: 'trace_id', key: 'trace_id', width: 130, render: (id: string, r: any) => id ? <a onClick={() => navigate(`/traces/${id}`)} style={{ fontFamily: 'monospace' }}>{(id || '').slice(0, 12)}...</a> : (r?.start ? fmtLocalTime(r.start, '-', 'MM-DD HH:mm:ss') : '-') },
    { title: '发生时间', dataIndex: 'start', key: 'start', width: 160, render: (v: string) => fmtLocalTime(v, '-', 'MM-DD HH:mm:ss') },
    { title: '状态', dataIndex: 'errors', key: 'errors', width: 80, align: 'center' as const, render: (v: number) => v > 0 ? <Badge color="#e0455b" text="错误" /> : <Badge color="#2f9e5f" text="正常" /> },
    { title: '响应时间', dataIndex: 'max_ms', key: 'max_ms2', width: 100, align: 'right' as const, render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{`${v?.toFixed?.(2) ?? v}ms`}</span> },
  ]
  const spanColumns = [
    { title: '发生时间', dataIndex: 'start_time', key: 'start_time', render: (v: string) => fmtLocalTime(v) },
    { title: '接口', dataIndex: 'operation_name', key: 'operation_name' },
    { title: '响应时间', dataIndex: 'ms', key: 'ms', render: (v: number) => `${v?.toFixed?.(2) ?? v}ms` },
    { title: '状态', dataIndex: 'is_error', key: 'is_error', render: (v: number) => v ? <Badge color="#e0455b" text="错误" /> : <Badge color="#2f9e5f" text="正常" /> },
    { title: '请求地址', dataIndex: 'http_url', key: 'http_url', ellipsis: true },
  ]

  return (
    <>
      <Spin spinning={loading}>
        <Card
          title="服务拓扑"
          extra={
            <Space size={10}>
              <Button size="small" icon={<ReloadOutlined />} onClick={() => load(true)}>同步 Trace 数据</Button>
              <Tooltip title="刷新"><Button size="small" type="text" icon={<ReloadOutlined />} onClick={() => load()} /></Tooltip>
              <Tooltip title="下载 PNG"><Button size="small" type="text" icon={<DownloadOutlined />} onClick={handleDownload} /></Tooltip>
              <Tooltip title="全屏"><Button size="small" type="text" icon={<FullscreenOutlined />} onClick={handleFullscreen} /></Tooltip>
            </Space>
          }
          styles={{ body: { padding: 0 } }}
        >
          {/* 工具栏：类型过滤 + 关系可见性 + hide orphans */}
          <div style={{ padding: '8px 16px', borderBottom: '1px solid rgba(255,255,255,0.06)', display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
            <Button
              size="small"
              type={onlyAbnormal ? 'primary' : 'default'}
              danger={onlyAbnormal}
              onClick={() => setOnlyAbnormal(v => !v)}
            >
              {onlyAbnormal ? '显示全部' : '只看异常'}
            </Button>
            <Select
              size="small" style={{ width: 140 }} placeholder="按业务筛选"
              value={typeFilter || undefined}
              onChange={setTypeFilter}
              allowClear
              options={typeChips.map(([t, c]) => ({ value: t, label: `${typeTierLabel(t)} · ${t} (${c})` }))}
            />
            <Select
              size="small" style={{ width: 130 }}
              value={timeRange}
              onChange={setTimeRange}
              options={[
                { value: 15, label: '近 15 分钟' },
                { value: 60, label: '近 1 小时' },
                { value: 1440, label: '近 24 小时' },
              ]}
            />
            <Button size="small" onClick={() => setFocusNodeId(focusTarget?.id ?? null)}>自动聚焦</Button>
            <div style={{ flex: 1 }} />
            <Text style={{ fontSize: 11, color: 'rgba(255,255,255,0.35)' }}>
              {nodes.length} 节点 · {relations.length} 关系
            </Text>
            <Collapse
              ghost size="small"
              style={{ fontSize: 12 }}
              items={[{
                key: 'adv',
                label: '高级设置',
                children: (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingTop: 4 }}>
                    <Space>
                      <Switch size="small" checked={hideOrphans} onChange={setHideOrphans} />
                      <Text style={{ fontSize: 12 }}>隐藏孤立节点</Text>
                    </Space>
                    {relationTypes.length > 0 && (
                      <PopoverRelTypes relationTypes={relationTypes} visible={visibleRelTypes} onChange={setVisibleRelTypes} />
                    )}
                  </div>
                ),
              }]}
            />
          </div>

          {/* 自动摘要条（人话）*/}
          {!loading && (
            <div style={{ padding: '8px 16px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
              <Alert
                type={summary.type}
                showIcon
                banner
                message={summary.message}
                style={{ borderRadius: 6 }}
              />
            </div>
          )}

          <div
            ref={wrapRef}
            style={{ position: 'relative', width: '100%', height: 'calc(100vh - 220px)', minHeight: 560, background: 'radial-gradient(circle at 50% 40%, rgba(22,119,255,0.06), transparent 60%)' }}
          >
            <div style={{ width: '100%', height: '100%' }}>
              {nodes.length === 0 ? (
                <Empty description="暂无拓扑数据（请先通过目录管理或数据同步录入节点与关系）" style={{ marginTop: 80 }} />
              ) : (
                <TopologyGraph
                  nodes={nodes}
                  relations={relations}
                  relationTypes={relationTypes}
                  selectedName={selectedNode?.name}
                  hideOrphans={hideOrphans}
                  visibleRelationTypes={visibleRelTypes || undefined}
                  metrics={nodeMetrics}
                  onlyAbnormal={onlyAbnormal}
                  abnormalNames={abnormalNames}
                  focusNodeId={focusNodeId}
                  onSelect={openNodeDetail}
                />
              )}
            </div>
            <div style={{ position: 'absolute', right: 16, bottom: 14 }}>
              <Text style={{ fontSize: 11, color: 'rgba(255,255,255,0.35)' }}>点击节点查看详情</Text>
            </div>
          </div>
        </Card>
      </Spin>

      {/* 详情抽屉 */}
      <Drawer
        title={
          <Space direction="vertical" size={0} style={{ display: 'flex' }}>
            <Space>
              <Text style={{ fontSize: 15, color: '#e8edf5', fontWeight: 600 }}>{selectedNode?.name}</Text>
            </Space>
            {selectedNode?.type && (
              <Text style={{ fontSize: 12, color: 'rgba(255,255,255,0.5)' }}>类型：{selectedNode.type}</Text>
            )}
          </Space>
        }
        placement="right"
        width={680}
        onClose={() => setDrawerOpen(false)}
        open={drawerOpen}
        styles={{ body: { background: '#0f1724', padding: 16 }, header: { background: '#141d2e', borderBottom: '1px solid rgba(255,255,255,0.08)' } }}
        extra={selectedNode?.name ? <Button type="primary" size="small" onClick={() => navigate(`/services/${encodeURIComponent(selectedNode.name)}`)}>查看系统详情</Button> : undefined}
      >
        <Spin spinning={detailLoading}>
          {selectedNode && describeNode(selectedNode, nodeMetrics, relations, nodes).status !== '未知' && (
            <div style={{ marginBottom: 16, padding: '12px 14px', borderRadius: 8, background: '#161e2e', border: '1px solid rgba(255,255,255,0.08)' }}>
              <Space style={{ marginBottom: 4 }}>
                <Badge color={describeNode(selectedNode, nodeMetrics, relations, nodes).status === '异常' ? '#e0455b' : '#2f9e5f'}
                  text={<Text style={{ fontSize: 13, fontWeight: 600, color: describeNode(selectedNode, nodeMetrics, relations, nodes).status === '异常' ? '#ff6b6b' : '#52c41a' }}>
                    {describeNode(selectedNode, nodeMetrics, relations, nodes).status}
                  </Text>} />
                <Text style={{ fontSize: 13, color: 'rgba(255,255,255,0.85)' }}>
                  {describeNode(selectedNode, nodeMetrics, relations, nodes).sentence}
                </Text>
              </Space>
              <div style={{ fontSize: 12, color: 'rgba(255,255,255,0.55)' }}>
                调用 {describeNode(selectedNode, nodeMetrics, relations, nodes).downCount} 个服务 · 被 {describeNode(selectedNode, nodeMetrics, relations, nodes).upCount} 个服务调用
              </div>
            </div>
          )}
          {nodeDetail && (
            <>
              <Row gutter={12} style={{ marginBottom: 16 }}>
                <Col span={6}><Card size="small" styles={{ body: { background: '#161e2e', border: '1px solid rgba(255,255,255,0.08)' } }}><Statistic title="Apdex" value={nodeDetail.apdex} precision={2} valueStyle={{ color: nodeDetail.apdex >= 0.9 ? '#2f9e5f' : nodeDetail.apdex >= 0.7 ? '#d98b1f' : '#e0455b' }} /></Card></Col>
                <Col span={6}><Card size="small" styles={{ body: { background: '#161e2e', border: '1px solid rgba(255,255,255,0.08)' } }}><Statistic title="响应时间" value={nodeDetail.latency_ms} suffix="ms" precision={2} valueStyle={{ color: nodeDetail.latency_ms > 1000 ? '#e0455b' : nodeDetail.latency_ms > 300 ? '#d98b1f' : '#4e9bff' }} /></Card></Col>
                <Col span={6}><Card size="small" styles={{ body: { background: '#161e2e', border: '1px solid rgba(255,255,255,0.08)' } }}><Statistic title="请求错误率" value={nodeDetail.error_rate} suffix="%" precision={2} valueStyle={{ color: nodeDetail.error_rate > 3 ? '#e0455b' : '#2f9e5f' }} /></Card></Col>
                <Col span={6}><Card size="small" styles={{ body: { background: '#161e2e', border: '1px solid rgba(255,255,255,0.08)' } }}><Statistic title="吞吐率" value={nodeDetail.throughput} suffix="rpm" valueStyle={{ color: '#73d13d' }} /></Card></Col>
              </Row>

              <Card size="small" title="指标趋势" extra={<Select size="small" value={metricType} onChange={setMetricType} style={{ width: 130 }} options={[
                { value: 'errorRate', label: '请求错误率' },
                { value: 'latency', label: '响应时间' },
                { value: 'calls', label: '调用量' },
                { value: 'errors', label: '错误数' },
              ]} />} styles={{ body: { background: '#161e2e' }, header: { background: '#1a2438', color: '#e8edf5' } }} style={{ marginBottom: 16, border: '1px solid rgba(255,255,255,0.08)' }}>
                <Text style={{ fontSize: 12, color: 'rgba(255,255,255,0.5)' }}>
                  {nodeDetail.metrics?.calls ? `共采集 ${nodeDetail.metrics.calls} 条调用，其中错误 ${nodeDetail.metrics.errors || 0} 条` : ''}
                </Text>
                <div style={{ height: 220, marginTop: 12 }}>
                  <ReactECharts option={trendOption} style={{ height: '100%' }} theme="dark" />
                </div>
              </Card>

              <Card size="small" styles={{ body: { background: '#161e2e', padding: 0 }, header: { background: '#1a2438', color: '#e8edf5' } }} style={{ border: '1px solid rgba(255,255,255,0.08)' }}>
                <Tabs defaultActiveKey="traces" items={[
                  { key: 'traces', label: '调用链', children: <Table size="small" columns={traceColumns} dataSource={nodeDetail.traces || []} rowKey="trace_id" pagination={{ pageSize: 5 }} scroll={{ x: 'max-content' }} /> },
                  { key: 'spans', label: 'Span 明细', children: <Table size="small" columns={spanColumns} dataSource={nodeDetail.spans || []} rowKey={(r: any, i) => `${r.operation_name}-${i}`} pagination={{ pageSize: 5 }} scroll={{ x: 'max-content' }} /> },
                ]} />
              </Card>
            </>
          )}
        </Spin>
      </Drawer>
    </>
  )
}

// 关系类型可见性 Popover
function PopoverRelTypes({
  relationTypes, visible, onChange,
}: {
  relationTypes: TopologyRelationTypeItem[]
  visible: Set<string> | null
  onChange(v: Set<string>): void
}) {
  const toggle = (name: string) => {
    const next = new Set(visible || relationTypes.map((rt) => rt.name))
    if (next.has(name)) next.delete(name)
    else next.add(name)
    onChange(next)
  }
  return (
    <Space size={4} wrap>
      {relationTypes.map((rt) => (
        <Checkbox key={rt.name} checked={visible?.has(rt.name) ?? true} onChange={() => toggle(rt.name)}>
          <Text style={{ fontSize: 11 }}>{rt.display_name || rt.name}</Text>
        </Checkbox>
      ))}
    </Space>
  )
}

export default Topology
