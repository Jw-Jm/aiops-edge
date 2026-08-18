import React, { useEffect, useRef, useState } from 'react'
import { Select, Button, Space, Spin, Statistic, Row, Col, Empty, Tag } from 'antd'
import * as echarts from 'echarts'
import { getCapacityForecast, getCapacityInstances, CapacityForecast } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'
import ErrorState from '../../components/ErrorState'

// A1: 区分"无数据"与"数值为 0"。后端空数据时返回 current:0 + 空 history（且后续版本带 has_data:false），
// 前端把 `current===0 && history 为空` 视为无数据（当前值/环比显示 --，图表显示空态）。
const hasForecastData = (d: CapacityForecast | null | undefined): boolean =>
  !!d &&
  (d as any).has_data !== false &&
  d.current !== null && d.current !== undefined &&
  !(d.current === 0 && (!d.history || d.history.length === 0))

// 2.15 容量预测：CPU / 内存 / 磁盘 三指标同页展示（无下拉切换），当前值保留 2 位小数
const METRICS: { key: 'cpu' | 'memory' | 'disk'; label: string; threshold: number }[] = [
  { key: 'cpu', label: 'CPU 使用率', threshold: 80 },
  { key: 'memory', label: '内存使用率', threshold: 90 },
  { key: 'disk', label: '磁盘使用率', threshold: 85 },
]

// P1: ETT（触达阈值时间）展示。基于 ewma 预测的 ett_seconds；已越阈值/超预测窗口时给出明确文案。
const ettText = (d: CapacityForecast | null): string => {
  if (!d) return '-'
  const ewma = d.forecasts?.ewma
  if (!ewma) return '-'
  if (ewma.already_breached) return '已触达阈值'
  if (!ewma.within_horizon || !ewma.ett_seconds || ewma.ett_seconds <= 0) return '预测窗口内不触达'
  const h = Math.floor(ewma.ett_seconds / 3600)
  const m = Math.floor((ewma.ett_seconds % 3600) / 60)
  return h > 0 ? `${h} 小时 ${m} 分后` : `${m} 分后`
}
const ettTone = (d: CapacityForecast | null): string => {
  if (!d) return 'var(--text-muted)'
  const ewma = d.forecasts?.ewma
  if (!ewma) return 'var(--text-muted)'
  if (ewma.already_breached) return 'var(--danger)'
  if (!ewma.within_horizon || !ewma.ett_seconds || ewma.ett_seconds <= 0) return 'var(--success)'
  return ewma.ett_seconds <= 24 * 3600 ? 'var(--warning)' : 'var(--text)'
}

const Capacity: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const chartRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const [instances, setInstances] = useState<string[]>([])
  const [instance, setInstance] = useState('')
  const [horizon, setHorizon] = useState(24)
  const [data, setData] = useState<Record<string, CapacityForecast | null>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    getCapacityInstances().then((r) => setInstances(r.data?.instances || [])).catch(() => {})
  }, [])

  const load = () => {
    setLoading(true)
    setErrors({})
    Promise.all(
      METRICS.map((m) =>
        getCapacityForecast({ metric: m.key, instance: instance || undefined, hours: 72, horizon, threshold: m.threshold })
          .then((r) => ({ key: m.key, d: r.data, err: '' }))
          .catch((e: any) => ({ key: m.key, d: null as CapacityForecast | null, err: e?.response?.data?.error || e?.message || '加载失败' }))
      )
    ).then((results) => {
      const map: Record<string, CapacityForecast | null> = {}
      const errMap: Record<string, string> = {}
      results.forEach((r) => { map[r.key] = r.d; if (r.err) errMap[r.key] = r.err })
      setData(map)
      setErrors(errMap)
    }).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [instance, horizon, currentClusterId])

  useEffect(() => {
    if (!Object.keys(data).length) return
    const charts: echarts.ECharts[] = []
    METRICS.forEach((m) => {
      const d = data[m.key]
      const el = chartRefs.current[m.key]
      // A1: 无数据（current==0 且 history 为空）时不在图表区渲染空折线图，空态由 JSX 层 Empty 展示
      if (!d || !hasForecastData(d) || !el) return
      const ch = echarts.getInstanceByDom(el) || echarts.init(el)
      const x = d.timestamps.map((t) => new Date(t * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }))
      ch.setOption({
        tooltip: { trigger: 'axis' },
        legend: { bottom: 0 },
        grid: { left: 40, right: 20, top: 30, bottom: 40 },
        xAxis: { type: 'category', data: x, axisLabel: { color: '#7a8794' } },
        yAxis: { type: 'value', axisLabel: { color: '#7a8794' }, splitLine: { lineStyle: { color: '#eef2f7' } } },
        series: [
          { name: '历史', type: 'line', data: d.history, symbol: 'none', itemStyle: { color: '#2f54eb' } },
          { name: '线性预测', type: 'line', data: d.forecasts.linear.values, symbol: 'none', lineStyle: { type: 'dashed' }, itemStyle: { color: '#16a34a' } },
          { name: 'EWMA 预测', type: 'line', data: d.forecasts.ewma.values, symbol: 'none', lineStyle: { type: 'dotted' }, itemStyle: { color: '#d97706' } },
          { name: '阈值', type: 'line', data: d.history.map(() => d.threshold), symbol: 'none', lineStyle: { type: 'dashed' }, itemStyle: { color: '#dc2626' } },
        ],
      })
      charts.push(ch)
    })
    // 修复响应式：窗口缩放时三张图表跟随 resize；effect 重跑时移除旧监听，避免重复注册
    if (!charts.length) return
    const onResize = () => { charts.forEach((c) => { try { c.resize() } catch { /* ignore */ } }) }
    window.addEventListener('resize', onResize)
    return () => { window.removeEventListener('resize', onResize) }
  }, [data])

  // 组件卸载时释放所有图表实例，避免内存泄漏
  useEffect(() => {
    return () => {
      Object.values(chartRefs.current).forEach((el) => {
        if (el) { try { echarts.getInstanceByDom(el)?.dispose() } catch { /* ignore */ } }
      })
    }
  }, [])

  return (
    <div>
      <Breadcrumb items={[{ t: '容量与资源' }, { t: '容量预测' }]} />
      <PageHeader title="容量预测" desc="CPU / 内存 / 磁盘 使用趋势同屏展示，提前规避容量风险"
        actions={<Space wrap>
          {/* A8: 范围选择器 —— 集群聚合（instance=''）与节点级分组，明确区分范围 */}
          <Select placeholder="节点" style={{ width: 220 }} value={instance} onChange={setInstance}>
            <Select.Option value="">集群聚合（全部节点平均）</Select.Option>
            <Select.OptGroup label="节点级">
              {instances.map((i) => <Select.Option key={i} value={i}>{i}</Select.Option>)}
            </Select.OptGroup>
          </Select>
          <Select value={horizon} onChange={setHorizon} style={{ width: 130 }} options={[12, 24, 48].map((h) => ({ value: h, label: `预测未来 ${h} 小时` }))} />
          <Button icon={<span>↻</span>} onClick={load}>刷新</Button>
        </Space>} />

      {/* A8: 当前查询范围标识，避免"全部节点"标签误导 */}
      <div style={{ marginBottom: 12 }}>
        <Tag color={instance ? 'blue' : 'geekblue'}>范围：{instance ? `节点 ${instance}` : '集群聚合（全部节点）'}</Tag>
      </div>

      <Spin spinning={loading}>
        {METRICS.map((m) => {
          const d = data[m.key]
          // A1: 请求失败显示 ErrorState + 重试，而不是静默显示 0
          if (errors[m.key]) {
            return (
              <div className="card" style={{ padding: 0, marginBottom: 16 }} key={m.key}>
                <ErrorState message={errors[m.key]} onRetry={load} />
              </div>
            )
          }
          const ok = hasForecastData(d)
          const cur = ok ? d!.current : null
          const chg = ok ? d!.change_pct : null
          return (
            <div className="card" style={{ padding: 16, marginBottom: 16 }} key={m.key}>
              <Row gutter={[16, 16]} style={{ marginBottom: 12 }}>
                <Col xs={12} sm={12} md={6}><Statistic title={`${m.label} · 当前值`} value={cur ?? '--'} precision={cur == null ? undefined : 2} suffix={cur == null ? undefined : '%'} valueStyle={{ color: cur != null && cur > m.threshold ? 'var(--danger)' : 'var(--text)' }} /></Col>
                <Col xs={12} sm={12} md={6}><Statistic title="阈值" value={m.threshold} suffix="%" /></Col>
                <Col xs={12} sm={12} md={6}><Statistic title="环比变化" value={chg ?? '--'} precision={chg == null ? undefined : 2} suffix={chg == null ? undefined : '%'} valueStyle={{ color: chg != null && chg > 0 ? 'var(--danger)' : 'var(--success)' }} /></Col>
                <Col xs={12} sm={12} md={6}>
                  <Statistic title="预计触达阈值" value={ok ? ettText(d) : '--'} valueStyle={{ color: ok ? ettTone(d) : 'var(--text-muted)' }} />
                </Col>
              </Row>
              {ok ? (
                <div ref={(el) => { chartRefs.current[m.key] = el }} style={{ width: '100%', height: 300 }} />
              ) : (
                // A1: 历史为空时显示空态，而非空折线图
                <div style={{ width: '100%', height: 300, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <Empty description="暂无历史数据" />
                </div>
              )}
            </div>
          )
        })}
      </Spin>
    </div>
  )
}

export default Capacity
