import React, { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Card, Spin, Empty, Typography, Space, Badge, Select, Button, Tooltip,
  Drawer, Table, Tabs, DatePicker, Input, Popover, Statistic, Row, Col,
} from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import {
  ReloadOutlined, DownloadOutlined, FullscreenOutlined,
  InfoCircleOutlined, GatewayOutlined, DatabaseOutlined, ApartmentOutlined,
  ThunderboltOutlined, ClusterOutlined, GlobalOutlined, SearchOutlined,
} from '@ant-design/icons'
import { Graph, NodeEvent } from '@antv/g6'
import ReactECharts from 'echarts-for-react'
import * as echarts from 'echarts'
import html2canvas from 'html2canvas'
import { getTopology, getTopologyNodeDetail } from '../../api/client'
import { fmtLocalTime, fmtLocalHM } from '../../utils/date'

const { Text } = Typography
const { RangePicker } = DatePicker

// 节点类型 → 图标 / 颜色 / 分层标题
const TYPE_META: Record<string, { icon: React.ReactNode; color: string; label: string }> = {
  external: { icon: <GlobalOutlined />, color: '#7c8db5', label: '外部/客户端' },
  gateway: { icon: <GatewayOutlined />, color: '#36cfc9', label: '网关' },
  service: { icon: <ApartmentOutlined />, color: '#4096ff', label: '业务服务' },
  db: { icon: <DatabaseOutlined />, color: '#b37feb', label: '数据库' },
  cache: { icon: <ThunderboltOutlined />, color: '#ffa940', label: '缓存' },
  mq: { icon: <ClusterOutlined />, color: '#73d13d', label: '消息队列' },
}

const HEALTH_COLOR: Record<string, string> = {
  healthy: '#2f9e5f',
  warning: '#d98b1f',
  error: '#e0455b',
  unknown: '#5b6b8c',
}

const HEALTH_LEVEL_COLOR: Record<string, string> = {
  normal: '#2f9e5f',
  slight: '#d98b1f',
  severe: '#e0455b',
  unknown: '#5b6b8c',
}

const LATENCY_COLOR: Record<string, string> = {
  fast: '#2f9e5f',
  slow: '#d98b1f',
  very_slow: '#e0455b',
}

interface TopoNode {
  name: string
  type: string
  rank: number
  calls: number
  errs: number
  latency_ms: number
  error_rate: number
  health: string
  health_level: string
  health_score: number
  throughput: number
}
interface TopoEdge {
  source_service: string
  target_service: string
  calls: number
  error_count: number
  latency_ms: number
  error_rate: number
  latency_level: string
}

const Topology: React.FC = () => {
  const containerRef = useRef<HTMLDivElement>(null)
  const graphRef = useRef<Graph | null>(null)
  const wrapRef = useRef<HTMLDivElement>(null)
  const navigate = useNavigate()

  const [nodes, setNodes] = useState<TopoNode[]>([])
  const [edges, setEdges] = useState<TopoEdge[]>([])
  const [loading, setLoading] = useState(true)
  const [timeRange, setTimeRange] = useState<[Dayjs, Dayjs]>([dayjs().subtract(15, 'minute'), dayjs()])
  const [system, setSystem] = useState('all')
  const [serviceQuery, setServiceQuery] = useState('')
  const [entityQuery, setEntityQuery] = useState('')
  const [depth, setDepth] = useState(5)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [selectedNode, setSelectedNode] = useState<TopoNode | null>(null)
  const [nodeDetail, setNodeDetail] = useState<any>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [legendOpen, setLegendOpen] = useState(false)
  // 指标选择：趋势图展示的指标类型
  const [metricType, setMetricType] = useState<'errorRate' | 'latency' | 'calls' | 'errors'>('errorRate')

  const minutes = useMemo(() => {
    return Math.max(1, timeRange[1].diff(timeRange[0], 'minute'))
  }, [timeRange])

  const load = () => {
    setLoading(true)
    getTopology({ minutes })
      .then((r) => {
        const raw = r.data?.data || r.data
        setNodes(Array.isArray(raw?.nodes) ? raw.nodes : [])
        setEdges(Array.isArray(raw?.edges) ? raw.edges : [])
      })
      .catch(() => {
        setNodes([])
        setEdges([])
      })
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [minutes])

  const stats = useMemo(() => {
    let severe = 0, slight = 0, healthy = 0, unknown = 0
    nodes.forEach((n) => {
      if (n.health_level === 'severe' || n.health === 'error') severe++
      else if (n.health_level === 'slight' || n.health === 'warning') slight++
      else if (n.health === 'healthy') healthy++
      else unknown++
    })
    return { severe, slight, healthy, unknown }
  }, [nodes])

  // 系统过滤 + 搜索过滤
  const systemFiltered = useMemo(() => {
    let fn = nodes
    if (system !== 'all' && nodes.length > 0) {
      const isDeepflow = (name: string) => name.startsWith('deepflow-') || name.startsWith('deepflow_')
      const match = system === 'deepflow' ? isDeepflow : (name: string) => !isDeepflow(name)
      fn = nodes.filter((n) => match(n.name))
    }
    if (serviceQuery.trim()) {
      const q = serviceQuery.trim().toLowerCase()
      fn = fn.filter((n) => n.name.toLowerCase().includes(q))
    }
    if (entityQuery.trim()) {
      const q = entityQuery.trim().toLowerCase()
      fn = fn.filter((n) => (TYPE_META[n.type]?.label || '').toLowerCase().includes(q) || n.name.toLowerCase().includes(q))
    }
    const fnSet = new Set(fn.map((n) => n.name))
    const fe = edges.filter((e) => fnSet.has(e.source_service) && fnSet.has(e.target_service))
    return { nodes: fn, edges: fe }
  }, [nodes, edges, system, serviceQuery, entityQuery])

  // 深度过滤：从最上游入口（入度最小/无入边）出发 BFS
  const filtered = useMemo(() => {
    const base = systemFiltered
    const { nodes: sn, edges: se } = base
    if (depth >= 5 || sn.length === 0) return base

    const adj = new Map<string, string[]>()
    const inDeg = new Map<string, number>()
    sn.forEach((n) => { inDeg.set(n.name, 0) })
    se.forEach((e) => {
      if (!adj.has(e.source_service)) adj.set(e.source_service, [])
      adj.get(e.source_service)!.push(e.target_service)
      if (inDeg.has(e.target_service)) inDeg.set(e.target_service, (inDeg.get(e.target_service) || 0) + 1)
    })
    let minDeg = Infinity
    sn.forEach((n) => { minDeg = Math.min(minDeg, inDeg.get(n.name) || 0) })
    const entries = sn.filter((n) => (inDeg.get(n.name) || 0) <= Math.min(minDeg, 1))

    const reach = new Set<string>()
    const queue: Array<[string, number]> = []
    entries.forEach((n) => { if (!reach.has(n.name)) { reach.add(n.name); queue.push([n.name, 0]) } })
    while (queue.length) {
      const [cur, d] = queue.shift()!
      if (d >= depth) continue
      ;(adj.get(cur) || []).forEach((next) => {
        if (!reach.has(next)) { reach.add(next); queue.push([next, d + 1]) }
      })
    }
    const fn = sn.filter((n) => reach.has(n.name))
    const fe = se.filter((e) => reach.has(e.source_service) && reach.has(e.target_service))
    return { nodes: fn, edges: fe }
  }, [systemFiltered, depth])

  const buildNodeHTML = (n: TopoNode) => {
    const meta = TYPE_META[n.type] || TYPE_META.service
    const color = HEALTH_LEVEL_COLOR[n.health_level] || HEALTH_LEVEL_COLOR.normal
    const pct = Math.min(100, Math.max(0, n.error_rate))
    // 外环：健康度颜色 + 错误率占比进度（用 conic-gradient 模拟）
    const ringStyle = pct > 0
      ? `background: conic-gradient(${color} ${pct}%, rgba(255,255,255,0.12) ${pct}%)`
      : `background: ${color}`
    // 指标行：延迟 / 错误率 / 吞吐，用更清晰的标签布局
    return `
      <div data-node-id="${n.name}" style="display:flex;flex-direction:column;align-items:center;width:148px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;cursor:pointer;user-select:none;">
        <div style="position:relative;width:76px;height:76px;display:flex;align-items:center;justify-content:center;">
          <div style="position:absolute;inset:0;border-radius:50%;${ringStyle};padding:4px;box-sizing:border-box;"></div>
          <div style="position:relative;width:62px;height:62px;border-radius:50%;background:#161e2e;border:1px solid rgba(255,255,255,0.15);display:flex;align-items:center;justify-content:center;color:${meta.color};font-size:30px;font-weight:700;box-shadow:0 2px 8px rgba(0,0,0,0.3);">
            ${meta.label[0]}
          </div>
        </div>
        <div style="margin-top:8px;text-align:center;line-height:1.5;width:100%;">
          <div style="font-size:11px;color:rgba(255,255,255,0.55);white-space:nowrap;display:flex;justify-content:center;gap:6px;">
            <span style="color:${n.latency_ms > 1000 ? '#e0455b' : n.latency_ms > 300 ? '#d98b1f' : 'rgba(255,255,255,0.65)'}">${n.latency_ms}ms</span>
            <span style="color:${n.error_rate > 3 ? '#e0455b' : 'rgba(255,255,255,0.65)'}">${n.error_rate}%</span>
            <span style="color:rgba(255,255,255,0.5)">${n.throughput}rpm</span>
          </div>
          <div style="font-size:14px;color:#e8edf5;font-weight:600;max-width:148px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-top:2px;" title="${n.name}">${n.name}</div>
        </div>
      </div>
    `
  }

  useEffect(() => {
    if (!containerRef.current) return
    const { nodes: fnodes, edges: fedges } = filtered
    if (fnodes.length === 0) return

    const maxCalls = Math.max(1, ...fnodes.map((n) => n.calls))

    const g6Nodes = fnodes.map((n) => ({
      id: n.name,
      data: { node: n },
      style: {
        type: 'html',
        innerHTML: buildNodeHTML(n),
        dx: -74,
        dy: -55,
        size: [148, 110],
      } as any,
    }))

    const g6Edges = fedges.map((e, i) => {
      const color = LATENCY_COLOR[e.latency_level] || LATENCY_COLOR.fast
      return {
        id: `${e.source_service}->${e.target_service}-${i}`,
        source: e.source_service,
        target: e.target_service,
        data: { edge: e },
        style: {
          stroke: color,
          lineWidth: 1 + Math.min(3, (e.calls / maxCalls) * 2),
          endArrow: true,
          endArrowSize: 6,
          labelText: `${e.latency_ms}ms`,
          labelFill: color,
          labelFontSize: 10,
          labelPlacement: 'center',
          labelOffsetY: -8,
        } as any,
      }
    })

    if (graphRef.current) {
      graphRef.current.destroy()
      graphRef.current = null
    }

    const graph = new Graph({
      container: containerRef.current,
      data: { nodes: g6Nodes, edges: g6Edges },
      layout: {
        type: 'antv-dagre',
        rankdir: 'LR',
        align: 'UL',
        nodesep: 45,
        ranksep: 180,
        controlPoints: true,
      } as any,
      node: {
        type: 'html',
        style: {
          innerHTML: (d: any) => d.style?.innerHTML ?? '<div></div>',
          dx: (d: any) => d.style?.dx ?? 0,
          dy: (d: any) => d.style?.dy ?? 0,
          size: (d: any) => d.style?.size ?? [148, 110],
        },
        state: {
          highlighted: { opacity: 1 },
          inactive: { opacity: 0.25 },
        },
      },
      edge: {
        type: 'polyline',
        style: {
          stroke: (d: any) => d.style?.stroke ?? '#2f9e5f',
          lineWidth: (d: any) => d.style?.lineWidth ?? 1.5,
          endArrow: true,
          endArrowSize: 6,
          labelText: (d: any) => d.style?.labelText ?? '',
          labelFill: (d: any) => d.style?.labelFill ?? '#2f9e5f',
          labelFontSize: 10,
          labelPlacement: 'center',
        },
        state: {
          highlighted: { stroke: '#1677ff', lineWidth: 3.5, labelFill: '#1677ff' },
          inactive: { stroke: 'rgba(78,155,255,0.08)' },
        },
      },
      behaviors: [
        'drag-canvas',
        'zoom-canvas',
        'drag-element',
        { type: 'hover-activate', degree: 1, state: 'highlighted', inactiveState: 'inactive' },
      ],
      animation: true,
      autoFit: 'view',
      padding: 20,
    })

    graph.render()
    graphRef.current = graph

    // 使用 G6 原生节点点击事件（比 DOM 绑定更可靠）
    graph.on(NodeEvent.CLICK, (e: any) => {
      const id = e?.target?.id || e?.target?.data?.id || e?.item?.id
      const node = fnodes.find((n) => n.name === id)
      if (node) openNodeDetail(node)
    })

    // 兜底：渲染后给每个节点 DOM 绑定 click（兼容 HTML 节点事件未冒泡的情况）
    const bindNodeClicks = () => {
      containerRef.current?.querySelectorAll('[data-node-id]').forEach((el) => {
        const htmlEl = el as HTMLElement
        if ((htmlEl as any).__topologyBound) return
        ;(htmlEl as any).__topologyBound = true
        htmlEl.addEventListener('click', (ev) => {
          ev.stopPropagation()
          const id = htmlEl.dataset.nodeId
          if (!id) return
          const node = fnodes.find((n) => n.name === id)
          if (node) openNodeDetail(node)
        })
      })
    }
    // 动画渲染后延迟绑定兜底事件
    setTimeout(bindNodeClicks, 300)

    return () => {
      graph.destroy()
      graphRef.current = null
    }
  }, [filtered])

  const openNodeDetail = async (node: TopoNode) => {
    setSelectedNode(node)
    setDrawerOpen(true)
    setDetailLoading(true)
    try {
      const res = await getTopologyNodeDetail(node.name, { minutes })
      setNodeDetail(res.data?.data || res.data)
    } catch (e) {
      setNodeDetail(null)
    } finally {
      setDetailLoading(false)
    }
  }

  // 暴露给 G6 HTML 节点 onclick 使用
  useEffect(() => {
    ;(window as any).__openTopologyNode = (name: string) => {
      const node = nodes.find((n) => n.name === name)
      if (node) openNodeDetail(node)
    }
    return () => { delete (window as any).__openTopologyNode }
  }, [nodes, minutes])

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
      const canvas = await html2canvas(target, {
        backgroundColor: '#0b1220',
        scale: 2,
        useCORS: true,
        logging: false,
      })
      const url = canvas.toDataURL('image/png')
      const a = document.createElement('a')
      a.href = url
      a.download = `topology-${dayjs().format('YYYYMMDD-HHmmss')}.png`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch (e) {
      console.error('[topology] download failed:', e)
    }
  }

  // 指标选择配置：每种指标对应的图例、数据提取与格式化
  // series 支持 yAxisIndex（0=左轴，1=右轴）与 type（bar/line）。
  // 双轴场景：错误率/延迟等小量级用 line（左轴），调用量/错误数等大量级用 bar（右轴），避免量级悬殊导致错乱。
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

  // 大数字格式化：12345 -> 12.35K（用于 tooltip / y 轴调用量）
  const fmtBig = (n: number): string => {
    if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M'
    if (n >= 1e3) return (n / 1e3).toFixed(2) + 'K'
    return String(Math.round(n))
  }

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
        itemStyle: {
          color: s.color,
          borderRadius: isLine ? undefined : (isRight ? [4, 4, 0, 0] : [2, 2, 0, 0]),
        },
        barMaxWidth: dual ? (isRight ? 14 : 10) : 24,
      }
      if (isLine) {
        // 折线：平滑曲线 + 渐变面积，突出趋势
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
        // 右侧柱状（调用量）：半透明弱化，作为背景对比
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
          {
            ...axisBase, name: cfg.yAxisName[0],
            nameTextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 10, padding: [0, 0, 0, 0] },
            axisLabel: { ...axisText, formatter: (v: number) => fmtBig(v) + '%' },
            splitLine: { show: true, lineStyle: { color: 'rgba(255,255,255,0.06)' } },
          },
          {
            ...axisBase, name: cfg.yAxisName[1],
            nameTextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 10 },
            axisLabel: { ...axisText, formatter: (v: number) => fmtBig(v) },
            splitLine: { show: false },
          },
        ]
      : {
          ...axisBase, name: cfg.yAxisName[0],
          nameTextStyle: { color: 'rgba(255,255,255,0.45)', fontSize: 10 },
          axisLabel: { ...axisText, formatter: (v: number) => fmtBig(v) + (cfg.unit === '%' ? '%' : '') },
        }

    // tooltip 每行显示对应单位
    const formatter = (params: any) => {
      const arr = Array.isArray(params) ? params : [params]
      const header = `<div style="font-weight:600;margin-bottom:4px;color:#fff">${arr[0]?.axisValue || ''}</div>`
      const rows = arr.map((p: any) => {
        // 依据序列名判定单位：百分比 / 毫秒 / 次数
        let val: string
        if (p.seriesName === '请求错误率') {
          val = Number(p.value).toFixed(2) + '%'
        } else if (p.seriesName === '平均响应时间') {
          val = Number(p.value).toFixed(2) + ' ms'
        } else {
          val = fmtBig(Number(p.value))
        }
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
      legend: {
        data: cfg.series.map((s) => s.name),
        textStyle: { color: 'rgba(255,255,255,0.6)', fontSize: 11 },
        bottom: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 8,
        itemGap: 16,
      },
      xAxis: {
        type: 'category', data: x,
        axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } },
        axisLabel: { color: 'rgba(255,255,255,0.5)', fontSize: 10 },
        axisTick: { show: false },
      },
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

  const legendContent = (
    <div style={{ width: 260, padding: 4 }}>
      <div style={{ marginBottom: 12 }}>
        <div style={{ fontSize: 13, fontWeight: 600, color: '#e8edf5', marginBottom: 8 }}>健康评分染色图例</div>
        {[
          { color: HEALTH_LEVEL_COLOR.normal, label: '健康度正常节点' },
          { color: HEALTH_LEVEL_COLOR.slight, label: '健康度轻微异常节点' },
          { color: HEALTH_LEVEL_COLOR.severe, label: '健康度严重异常节点' },
          { gradient: true, label: '外环颜色表示错误率占比' },
        ].map((item, idx) => (
          <div key={idx} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
            {item.gradient ? (
              <div style={{ width: 14, height: 14, borderRadius: '50%', background: 'conic-gradient(#e0455b 60%, rgba(255,255,255,0.15) 60%)', border: '2px solid rgba(255,255,255,0.2)' }} />
            ) : (
              <div style={{ width: 14, height: 14, borderRadius: '50%', background: item.color, border: '2px solid rgba(255,255,255,0.2)' }} />
            )}
            <Text style={{ fontSize: 12, color: 'rgba(255,255,255,0.65)' }}>{item.label}</Text>
          </div>
        ))}
      </div>
      <div>
        <div style={{ fontSize: 13, fontWeight: 600, color: '#e8edf5', marginBottom: 8 }}>调用连线染色图例</div>
        {[
          { color: LATENCY_COLOR.fast, label: '响应时间小于较慢阈值' },
          { color: LATENCY_COLOR.slow, label: '响应时间大于较慢阈值，小于很慢阈值' },
          { color: LATENCY_COLOR.very_slow, label: '响应时间大于很慢阈值' },
        ].map((item, idx) => (
          <div key={idx} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
            <div style={{ width: 24, height: 3, background: item.color }} />
            <Text style={{ fontSize: 12, color: 'rgba(255,255,255,0.65)' }}>{item.label}</Text>
          </div>
        ))}
      </div>
    </div>
  )

  if (!loading && nodes.length === 0) {
    return (
      <Card title="全局服务拓扑">
        <Empty description="暂无拓扑数据（请先在服务列表页执行数据同步）" />
      </Card>
    )
  }

  return (
    <>
      <Spin spinning={loading}>
        <Card
          title="全局服务拓扑"
          extra={
          <Space size={10}>
            <Badge color={HEALTH_COLOR.error} text={<Text style={{ fontSize: 12 }}>严重 {stats.severe}</Text>} />
            <Badge color={HEALTH_COLOR.warning} text={<Text style={{ fontSize: 12 }}>轻微 {stats.slight}</Text>} />
            <Badge color={HEALTH_COLOR.healthy} text={<Text style={{ fontSize: 12 }}>健康 {stats.healthy}</Text>} />
            <Badge color={HEALTH_COLOR.unknown} text={<Text style={{ fontSize: 12 }}>无评分 {stats.unknown}</Text>} />
          </Space>
        }
        styles={{ body: { padding: 0 } }}
      >
        <div
          ref={wrapRef}
          style={{ position: 'relative', width: '100%', height: 'calc(100vh - 200px)', minHeight: 560, background: 'radial-gradient(circle at 50% 40%, rgba(22,119,255,0.06), transparent 60%)' }}
        >
          {/* 顶部工具栏 */}
          <div style={{ padding: '10px 16px', borderBottom: '1px solid rgba(255,255,255,0.06)', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <Select value={system} onChange={setSystem} size="small" style={{ width: 150 }} options={[
              { value: 'all', label: '全部系统' },
              { value: 'observability', label: 'observability' },
              { value: 'deepflow', label: 'deepflow' },
            ]} />
            <Input size="small" placeholder="筛选服务名" prefix={<SearchOutlined />} value={serviceQuery} onChange={(e) => setServiceQuery(e.target.value)} style={{ width: 150 }} />
            <Input size="small" placeholder="筛选实体类型" prefix={<SearchOutlined />} value={entityQuery} onChange={(e) => setEntityQuery(e.target.value)} style={{ width: 150 }} />
            <Select value={depth} onChange={setDepth} size="small" style={{ width: 140 }} options={[
              { value: 1, label: '深度 1' },
              { value: 2, label: '深度 2' },
              { value: 3, label: '深度 3' },
              { value: 5, label: '全部调用深度' },
            ]} />
            <RangePicker
              size="small"
              showTime={{ format: 'HH:mm' }}
              format="MM-DD HH:mm"
              value={timeRange}
              onChange={(vals) => vals && setTimeRange(vals as [Dayjs, Dayjs])}
              style={{ width: 280 }}
            />
            <div style={{ flex: 1 }} />
            <Space size={2}>
              <Tooltip title="刷新"><Button size="small" type="text" aria-label="refresh" icon={<ReloadOutlined />} onClick={load} /></Tooltip>
              <Tooltip title="下载 PNG"><Button size="small" type="text" aria-label="download-png" icon={<DownloadOutlined />} onClick={handleDownload} /></Tooltip>
              <Tooltip title="全屏"><Button size="small" type="text" aria-label="fullscreen" icon={<FullscreenOutlined />} onClick={handleFullscreen} /></Tooltip>
              <Popover content={legendContent} title="图例" trigger="click" open={legendOpen} onOpenChange={setLegendOpen} placement="bottomRight">
                <Button size="small" type="text" aria-label="legend" icon={<InfoCircleOutlined />} />
              </Popover>
            </Space>
          </div>

          {/* 画布 */}
          <div ref={containerRef} style={{ width: '100%', height: 'calc(100% - 49px)', overflow: 'hidden' }} />

          {/* 提示 */}
          <div style={{ position: 'absolute', right: 16, bottom: 14 }}>
            <Text style={{ fontSize: 11, color: 'rgba(255,255,255,0.35)' }}>点击节点查看详情 · 悬停高亮链路</Text>
          </div>
        </div>
      </Card>
      </Spin>

      {/* 右侧详情抽屉 */}
      <Drawer
        title={
          <Space direction="vertical" size={0} style={{ display: 'flex' }}>
            <Space>
              <span style={{ background: HEALTH_LEVEL_COLOR[selectedNode?.health_level || 'normal'], color: '#fff', padding: '2px 8px', borderRadius: 4, fontSize: 13 }}>健康评分 {selectedNode?.health_score ?? 0}</span>
              <Text style={{ fontSize: 15, color: '#e8edf5', fontWeight: 600 }}>{selectedNode?.name}</Text>
            </Space>
            {selectedNode?.type && (
              <Text style={{ fontSize: 12, color: 'rgba(255,255,255,0.5)' }}>
                类型：{(TYPE_META[selectedNode.type] || TYPE_META.service).label}
              </Text>
            )}
          </Space>
        }
        placement="right"
        width={680}
        onClose={() => setDrawerOpen(false)}
        open={drawerOpen}
        styles={{ body: { background: '#0f1724', padding: 16 }, header: { background: '#141d2e', borderBottom: '1px solid rgba(255,255,255,0.08)' } }}
        extra={<Button type="primary" size="small" onClick={() => selectedNode && navigate(`/services/${encodeURIComponent(selectedNode.name)}`)}>查看系统详情</Button>}
      >
        <Spin spinning={detailLoading}>
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
                  在 {fmtLocalTime(timeRange[0].toISOString(), '')} ~ {fmtLocalTime(timeRange[1].toISOString(), '')}，共采集 {nodeDetail.metrics?.calls || 0} 条调用，其中错误 {nodeDetail.metrics?.errors || 0} 条（{METRIC_OPTIONS[metricType]?.label || '请求错误率'}）。
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

export default Topology
