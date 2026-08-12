import React, { useEffect, useRef, useState } from 'react'
import { Select, Button, Space, Spin, Statistic, Row, Col } from 'antd'
import * as echarts from 'echarts'
import { getCapacityForecast, getCapacityInstances, CapacityForecast } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'

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
  const chartRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const [instances, setInstances] = useState<string[]>([])
  const [instance, setInstance] = useState('')
  const [horizon, setHorizon] = useState(24)
  const [data, setData] = useState<Record<string, CapacityForecast | null>>({})
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    getCapacityInstances().then((r) => setInstances(r.data?.instances || [])).catch(() => {})
  }, [])

  const load = () => {
    setLoading(true)
    Promise.all(
      METRICS.map((m) =>
        getCapacityForecast({ metric: m.key, instance: instance || undefined, hours: 72, horizon, threshold: m.threshold })
          .then((r) => ({ key: m.key, d: r.data }))
          .catch(() => ({ key: m.key, d: null }))
      )
    ).then((results) => {
      const map: Record<string, CapacityForecast | null> = {}
      results.forEach((r) => { map[r.key] = r.d })
      setData(map)
    }).finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [instance, horizon])

  useEffect(() => {
    if (!Object.keys(data).length) return
    METRICS.forEach((m) => {
      const d = data[m.key]
      const el = chartRefs.current[m.key]
      if (!d || !el) return
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
    })
  }, [data])

  return (
    <div>
      <Breadcrumb items={[{ t: '容量与资源' }, { t: '容量预测' }]} />
      <PageHeader title="容量预测" desc="CPU / 内存 / 磁盘 使用趋势同屏展示，提前规避容量风险"
        actions={<Space wrap>
          <Select allowClear placeholder="节点" style={{ width: 160 }} value={instance || undefined} onChange={setInstance} options={[{ value: '', label: '全部节点' }, ...instances.map((i) => ({ value: i, label: i }))]} />
          <Select value={horizon} onChange={setHorizon} style={{ width: 130 }} options={[12, 24, 48].map((h) => ({ value: h, label: `预测未来 ${h} 小时` }))} />
          <Button icon={<span>↻</span>} onClick={load}>刷新</Button>
        </Space>} />

      <Spin spinning={loading}>
        {METRICS.map((m) => {
          const d = data[m.key]
          return (
            <div className="card" style={{ padding: 16, marginBottom: 16 }} key={m.key}>
              <Row gutter={[16, 16]} style={{ marginBottom: 12 }}>
                <Col span={6}><Statistic title={`${m.label} · 当前值`} value={d ? d.current : 0} precision={2} suffix="%" valueStyle={{ color: d && d.current > m.threshold ? 'var(--danger)' : 'var(--text)' }} /></Col>
                <Col span={6}><Statistic title="阈值" value={m.threshold} suffix="%" /></Col>
                <Col span={6}><Statistic title="环比变化" value={d ? d.change_pct : 0} precision={2} suffix="%" valueStyle={{ color: d && d.change_pct > 0 ? 'var(--danger)' : 'var(--success)' }} /></Col>
                <Col span={6}>
                  <Statistic title="预计触达阈值" value={ettText(d)} valueStyle={{ color: ettTone(d) }} />
                </Col>
              </Row>
              <div ref={(el) => { chartRefs.current[m.key] = el }} style={{ width: '100%', height: 300 }} />
            </div>
          )
        })}
      </Spin>
    </div>
  )
}

export default Capacity
