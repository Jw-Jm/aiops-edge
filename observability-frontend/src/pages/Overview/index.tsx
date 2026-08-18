import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Badge, Button, Col, Progress, Row, Segmented, Space, Tag, Tooltip } from 'antd'
import * as echarts from 'echarts'
import { useNavigate } from 'react-router-dom'
import {
  DashboardAlertEvent, DashboardResources, DashboardStats, NodeMetric,
  getAlertEvents, getDashboardResources, getDashboardStats, getNodeMetrics,
} from '../../api/client'
import { Breadcrumb, Empty, PageHeader, PaneCard, StatCard, StatusBadge } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const severityRank: Record<string, number> = { critical: 3, 严重: 3, warning: 2, 警告: 2, info: 1, 信息: 1 }
const severityLabel = (value?: string) => {
  const key = String(value || 'warning').toLowerCase()
  return severityRank[key] === 3 ? '严重' : severityRank[key] === 1 ? '信息' : '警告'
}
const severityTone = (value?: string): 'crit' | 'warn' | 'info' => severityRank[String(value || 'warning').toLowerCase()] === 3 ? 'crit' : severityRank[String(value || 'warning').toLowerCase()] === 1 ? 'info' : 'warn'
const isActive = (status?: string) => ['firing', 'acknowledged', ''].includes(String(status || '').toLowerCase())

function sparkPts(arr: number[], w = 120, h = 40): string {
  if (!arr || arr.length < 2) return ''
  const max = Math.max(...arr), min = Math.min(...arr), range = max - min || 1
  return arr.map((v, i) => `${((i / (arr.length - 1)) * w).toFixed(1)},${(h - ((v - min) / range) * (h - 4) - 2).toFixed(1)}`).join(' ')
}

const pct = (value?: number) => Number.isFinite(value) ? Math.max(0, Math.min(100, Number(value))) : null
const usageColor = (value?: number) => (value ?? 0) > 80 ? '#dc2626' : (value ?? 0) > 60 ? '#d97706' : '#16a34a'
const formatCapacity = (value?: number) => value == null || !Number.isFinite(value) ? '—' : `${value}`

const Overview: React.FC = () => {
  const navigate = useNavigate()
  const trendRef = useRef<HTMLDivElement | null>(null)
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const clusters = useUIStore((s) => s.clusters)
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [resources, setResources] = useState<DashboardResources | null>(null)
  const [nodes, setNodes] = useState<NodeMetric[]>([])
  const [alerts, setAlerts] = useState<DashboardAlertEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [nodeSort, setNodeSort] = useState<'cpu' | 'memory'>('cpu')
  const [showAllNodes, setShowAllNodes] = useState(false)

  // A6: 移除集群名映射 hack（c.id===1 ? 'default' : c.name），直接用集群 name 作为 cluster_id 字符串
  const clusterName = currentClusterId === 'all' ? '全部集群' : (clusters.find((c) => c.name === currentClusterId)?.name || currentClusterId)

  const load = () => {
    setLoading(true)
    Promise.all([
      getDashboardStats().then((r) => setStats(r.data)).catch(() => setStats(null)),
      getDashboardResources({ cluster_id: currentClusterId || 'all' }).then((r) => setResources(r.data)).catch(() => setResources(null)),
      getNodeMetrics().then((r) => setNodes(Array.isArray(r.data?.nodes) ? r.data.nodes : [])).catch(() => setNodes([])),
      // B12: 活跃告警 limit 由 200 提至 1000，覆盖大集群多规则场景，避免活跃告警被静默截断。
      // 后续应改为服务端分页统计（见 C2）。
      getAlertEvents({ limit: 1000 }).then((r) => {
        const data = r.data
        setAlerts(Array.isArray(data) ? data : (Array.isArray(data?.data) ? data.data : []))
      }).catch(() => setAlerts([])),
    ]).finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
    const timer = window.setInterval(load, 60000)
    return () => window.clearInterval(timer)
  }, [currentClusterId])

  const sortedNodes = useMemo(() => {
    const key = nodeSort === 'cpu' ? 'cpu_usage_pct' : 'mem_usage_pct'
    return [...nodes].sort((a, b) => (Number(b[key]) || 0) - (Number(a[key]) || 0))
  }, [nodes, nodeSort])
  const displayedNodes = showAllNodes ? sortedNodes : sortedNodes.slice(0, 5)
  const activeAlerts = useMemo(() => alerts.filter((a) => isActive(a.status)).sort((a, b) => (severityRank[String(b.severity || 'warning').toLowerCase()] || 2) - (severityRank[String(a.severity || 'warning').toLowerCase()] || 2)), [alerts])
  const trend = stats?.trend || []
  // A6: 服务数口径与拓扑视图一致（后端 /dashboard/stats 同时返回 services 与 topology_services，
  // 前者仅 trace 服务、后者含拓扑目录，总览卡片用 topology_services 才能与拓扑视图对得上）
  const topologyServices = (stats as any)?.topology_services ?? stats?.services ?? 0
  const hasDataGap = !!(stats?.data_gaps?.length)
  // A6: 各卡片使用自己的数据序列——服务数卡片用当前口径常量序列（趋势无逐桶服务数），不重复调用量序列
  const svcSparkSeries = trend.length > 1 ? Array.from({ length: trend.length }, () => topologyServices) : []
  const errRateSparkSeries = trend.map((t) => (t.calls ? t.errors / t.calls : 0))
  // A6: 数据采集中断时给统计卡加"数据不完整"角标，避免健康数值误导
  const withGapTag = (card: React.ReactNode) => (
    <div style={{ position: 'relative', height: '100%' }}>
      {card}
      {hasDataGap && (
        <Tag color="warning" style={{ position: 'absolute', top: 4, right: 4, fontSize: 10, lineHeight: '16px', margin: 0, borderRadius: 999 }}>
          数据不完整
        </Tag>
      )}
    </div>
  )

  useEffect(() => {
    const el = trendRef.current
    if (!el || !trend.length) return
    const chart = echarts.getInstanceByDom(el) || echarts.init(el)
    chart.setOption({
      animationDuration: 650, tooltip: { trigger: 'axis', confine: true },
      legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 8, textStyle: { color: '#52606d', fontSize: 12 } },
      grid: { left: 42, right: 48, top: 34, bottom: 28 },
      xAxis: { type: 'category', boundaryGap: false, data: trend.map((p) => p.t), axisLabel: { color: '#7a8794', fontSize: 11 } },
      yAxis: [
        { type: 'value', name: '调用量', nameTextStyle: { color: '#7a8794' }, axisLabel: { color: '#7a8794' }, splitLine: { lineStyle: { color: '#eef2f7' } } },
        { type: 'value', name: '错误率', nameTextStyle: { color: '#7a8794' }, axisLabel: { color: '#7a8794', formatter: '{value}%' }, splitLine: { show: false } },
      ],
      series: [
        { name: '调用量', type: 'line', smooth: true, symbol: 'none', data: trend.map((p) => p.calls), lineStyle: { width: 2, color: '#2f54eb' }, areaStyle: { color: 'rgba(47,84,235,.08)' } },
        { name: '错误率', type: 'line', yAxisIndex: 1, smooth: true, symbol: 'none', data: trend.map((p) => p.calls ? Number(((p.errors / p.calls) * 100).toFixed(2)) : 0), lineStyle: { width: 2, color: '#dc2626' } },
      ],
    })
    const resize = () => chart.resize()
    window.addEventListener('resize', resize)
    return () => { window.removeEventListener('resize', resize); chart.dispose() }
  }, [trend])

  const a = stats?.alerts
  return <div>
    <Breadcrumb items={[{ t: '总览' }, { t: '工作台首页' }]} />
    <PageHeader title="工作台首页" desc="当前集群资源态势一览 · 风险与健康一屏掌握" />
    {stats?.data_gaps?.length ? <Alert showIcon type="warning" message={`检测到数据采集中断：${stats.data_gaps.length} 个时段无数据`} style={{ marginBottom: 16 }} /> : null}

    <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
      <Col xs={12} md={6}>{withGapTag(<StatCard label="服务数量" value={loading ? '…' : topologyServices} unit="个" spark={sparkPts(svcSparkSeries)} sparkColor="var(--primary)" />)}</Col>
      <Col xs={12} md={6}>{withGapTag(<StatCard label="调用总量" value={loading ? '…' : (stats?.total_calls ?? 0).toLocaleString()} unit="次" spark={sparkPts(trend.map((t) => t.calls))} sparkColor="var(--primary)" />)}</Col>
      <Col xs={12} md={6}>{withGapTag(<StatCard label="请求错误率" value={loading ? '…' : `${(stats?.error_rate ?? 0).toFixed(2)}`} unit="%" trend={`${(stats?.error_rate ?? 0).toFixed(2)}%`} trendDir={(stats?.error_rate ?? 0) > 0 ? 'up' : 'flat'} spark={sparkPts(errRateSparkSeries)} sparkColor="var(--danger)" />)}</Col>
      <Col xs={12} md={6}>{withGapTag(<StatCard label="P95 延迟" value={loading ? '…' : `${(stats?.latency_p95 ?? 0).toFixed(1)}`} unit="ms" />)}</Col>
    </Row>

    <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
      <Col xs={24} lg={14}>
        <PaneCard title="资源态势" action={<Tag color="blue" style={{ borderRadius: 999 }}>{clusterName} · {resources?.node_count ?? 0} 节点</Tag>}>
          {resources?.resources?.length ? <Row gutter={[24, 20]}>{resources.resources.map((r) => {
            const current = pct(r.current ?? undefined), color = usageColor(current ?? undefined)
            const label = r.metric === 'cpu' ? 'CPU 使用率' : r.metric === 'memory' ? '内存使用率' : '磁盘使用率'
            return <Col xs={24} md={8} key={r.metric}><div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}><span style={{ color: 'var(--text-muted)', fontSize: 12 }}>{label}</span><b style={{ color }}>{current == null ? '—' : `${current.toFixed(1)}%`}</b></div><Progress percent={current ?? 0} showInfo={false} strokeColor={color} trailColor="var(--bg-soft)" size="small" /><div style={{ marginTop: 5, color: 'var(--text-muted)', fontSize: 11 }}>阈值 {r.threshold ?? '—'}% · {r.ett_seconds > 0 ? `预计 ${Math.round(r.ett_seconds / 3600)}h 后触达` : '预测窗口内不触达'}</div></Col>
          })}</Row> : <Empty text={loading ? '资源数据加载中…' : '暂无资源数据'} />}
        </PaneCard>
        <PaneCard title="节点资源 TOP5" action={<Space><Segmented size="small" value={nodeSort} onChange={(v) => setNodeSort(v as 'cpu' | 'memory')} options={[{ label: 'CPU', value: 'cpu' }, { label: '内存', value: 'memory' }]} />{nodes.length > 5 && <Button type="link" size="small" onClick={() => setShowAllNodes((v) => !v)}>{showAllNodes ? '收起' : `查看全部 (${nodes.length})`}</Button>}</Space>} style={{ marginTop: 16 }}>
          {displayedNodes.length ? <div style={{ overflowX: 'auto' }}><div style={{ minWidth: 600 }}><div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1.25fr 1.25fr .7fr .9fr', gap: 12, padding: '0 4px 8px', color: 'var(--text-muted)', fontSize: 11, borderBottom: '1px solid var(--border-soft)' }}><span>节点</span><span>CPU 使用率</span><span>内存使用率</span><span>CPU 核数</span><span>内存容量</span></div>{displayedNodes.map((n, i) => <div key={`${n.node || 'node'}-${i}`} style={{ display: 'grid', gridTemplateColumns: '1.2fr 1.25fr 1.25fr .7fr .9fr', gap: 12, alignItems: 'center', padding: '11px 4px', borderBottom: '1px solid var(--border-soft)', fontSize: 12 }}><span style={{ fontWeight: 600, whiteSpace: 'nowrap' }}>{n.node || '未命名节点'}</span><Progress percent={pct(n.cpu_usage_pct) ?? 0} strokeColor={usageColor(n.cpu_usage_pct)} format={(v) => `${v ?? 0}%`} size="small" /><Progress percent={pct(n.mem_usage_pct) ?? 0} strokeColor={usageColor(n.mem_usage_pct)} format={(v) => `${v ?? 0}%`} size="small" /><span>{formatCapacity(n.cpu_capacity)}</span><span>{formatCapacity(n.mem_capacity)}</span></div>)}</div></div> : <Empty text={loading ? '节点数据加载中…' : '暂无节点资源数据'} />}
        </PaneCard>
      </Col>
      <Col xs={24} lg={10}><PaneCard title="调用与错误趋势" action={<Tag color="blue" style={{ borderRadius: 999 }}>过去 24 小时</Tag>}>{trend.length ? <div ref={trendRef} style={{ height: 380, width: '100%' }} /> : <Empty text={loading ? '趋势数据加载中…' : '暂无趋势数据'} />}</PaneCard></Col>
    </Row>

    <PaneCard title={<span>活跃告警 <Badge count={activeAlerts.length} showZero style={{ backgroundColor: activeAlerts.length ? 'var(--danger)' : 'var(--success)', marginLeft: 8 }} /></span>} action={<Button type="link" onClick={() => navigate('/alerts/events')}>查看全部 →</Button>}>
      {activeAlerts.length ? <div style={{ overflowX: 'auto' }}><div style={{ minWidth: 900 }}>{activeAlerts.map((item, index) => { const question = `告警: ${item.rule_name || '未命名规则'} (${severityLabel(item.severity)}), 服务: ${item.service || '未知服务'}, 触发 ${item.count ?? 1} 次, 最近 ${item.last_timestamp || '未知时间'}, 消息: ${item.message || '无消息'}, 请分析根因并给出处置建议`; return <div key={`${item.rule_name}-${item.last_timestamp}-${index}`} style={{ display: 'grid', gridTemplateColumns: '1.15fr .8fr .65fr .45fr 1fr minmax(180px, 2fr) auto', gap: 14, alignItems: 'center', padding: '12px 4px', borderBottom: '1px solid var(--border-soft)', fontSize: 12 }}><span style={{ fontWeight: 600 }}>{item.rule_name || '未命名规则'}</span><span>{item.service || '—'}</span><StatusBadge text={severityLabel(item.severity)} tone={severityTone(item.severity)} /><span>{item.count ?? 1} 次</span><span style={{ color: 'var(--text-muted)' }}>{item.last_timestamp || '—'}</span><Tooltip title={item.message || '—'}><span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.message || '—'}</span></Tooltip><Button type="link" size="small" onClick={() => navigate(`/ai/chat?q=${encodeURIComponent(question)}`)}>根因定位</Button></div> })}</div></div> : <div style={{ textAlign: 'center', padding: '28px 8px', color: 'var(--success)', fontSize: 13 }}>✓ 当前无活跃告警，系统健康</div>}
    </PaneCard>
  </div>
}

export default Overview
