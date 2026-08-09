import React, { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Card, Spin, Descriptions, Statistic, Row, Col, Typography } from 'antd'
import AppEmpty from '../../components/AppEmpty'
import ReactECharts from 'echarts-for-react'
import { getServiceDetail } from '../../api/client'
import { fmtLocalHM } from '../../utils/date'

const { Title } = Typography

interface DataPoint {
  t: string; calls: number; errors: number; avg_ms: number
}

const ServiceDetail: React.FC<{ name?: string }> = ({ name: nameProp }) => {
  const { name: nameParam } = useParams<{ name: string }>()
  const name = nameProp || nameParam
  const [data, setData] = useState<DataPoint[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!name) return
    setLoading(true)
    getServiceDetail(name).then(r => {
      setData(Array.isArray(r.data) ? r.data : (r.data?.data || []))
    }).finally(() => setLoading(false))
  }, [name])

  const totalCalls = data.reduce((s, d) => s + (Number(d.calls) || 0), 0)
  const totalErrors = data.reduce((s, d) => s + (Number(d.errors) || 0), 0)
  const avgDuration = data.length > 0 ? data.reduce((s, d) => s + (Number(d.avg_ms) || 0), 0) / data.length : 0

  // 大数字单位换算：1234567 -> 1.23M
  const fmtNum = (n: number): string => {
    if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B'
    if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M'
    if (n >= 1e3) return (n / 1e3).toFixed(2) + 'K'
    return String(n)
  }

  // 深色 ECharts 基础样式（时间统一转本地时区）
  const axisStyle = { color: 'rgba(255,255,255,0.5)', fontSize: 10 }
  const splitLine = { lineStyle: { color: 'rgba(255,255,255,0.08)' } }
  const baseChart = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis' as const, backgroundColor: '#1a2233', borderColor: 'rgba(255,255,255,0.1)', textStyle: { color: '#fff' } },
  }
  const timeData = data.map(d => fmtLocalHM(d.t) || d.t)

  const callsOption = {
    ...baseChart,
    xAxis: { type: 'category' as const, data: timeData, axisLabel: { rotate: 40, fontSize: 10, color: 'rgba(255,255,255,0.5)' }, axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } } },
    yAxis: { type: 'value' as const, axisLabel: axisStyle, splitLine },
    series: [{ name: '调用量', type: 'bar', data: data.map(d => d.calls), itemStyle: { color: 'var(--primary)', borderRadius: [3, 3, 0, 0] }, barMaxWidth: 24 }],
    grid: { top: 20, right: 16, bottom: 34, left: 44 },
  }

  const durationOption = {
    ...baseChart,
    xAxis: { type: 'category' as const, data: timeData, axisLabel: { rotate: 40, fontSize: 10, color: 'rgba(255,255,255,0.5)' }, axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } } },
    yAxis: { type: 'value' as const, name: 'ms', nameTextStyle: { color: 'rgba(255,255,255,0.5)' }, axisLabel: axisStyle, splitLine },
    series: [{
      name: '平均响应时间 (ms)', type: 'line', data: data.map(d => Number(d.avg_ms?.toFixed(1) || 0)),
      smooth: true, symbol: 'circle', symbolSize: 4, itemStyle: { color: '#52c41a' },
      lineStyle: { color: '#52c41a', width: 2 }, areaStyle: { color: 'rgba(82,196,26,0.15)' }
    }],
    grid: { top: 20, right: 16, bottom: 34, left: 54 },
  }

  const errorOption = {
    ...baseChart,
    xAxis: { type: 'category' as const, data: timeData, axisLabel: { rotate: 40, fontSize: 10, color: 'rgba(255,255,255,0.5)' }, axisLine: { lineStyle: { color: 'rgba(255,255,255,0.1)' } } },
    yAxis: { type: 'value' as const, axisLabel: axisStyle, splitLine },
    series: [{ name: '错误数', type: 'bar', data: data.map(d => d.errors), itemStyle: { color: '#ff4d4f', borderRadius: [3, 3, 0, 0] }, barMaxWidth: 24 }],
    grid: { top: 20, right: 16, bottom: 34, left: 44 },
  }

  return (
    <Spin spinning={loading}>
      {!nameProp && <Title level={4} style={{ marginBottom: 16 }}>{name}</Title>}
      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col span={6}><Card size="small" styles={{ body: { padding: '16px 20px' } }}><Statistic title="总调用量" value={fmtNum(totalCalls)} valueStyle={{ color: '#4e9bff' }} /></Card></Col>
        <Col span={6}><Card size="small" styles={{ body: { padding: '16px 20px' } }}><Statistic title="错误数" value={fmtNum(totalErrors)} valueStyle={{ color: totalErrors > 0 ? '#ff4d4f' : '#52c41a' }} /></Card></Col>
        <Col span={6}><Card size="small" styles={{ body: { padding: '16px 20px' } }}><Statistic title="错误率" value={`${data.length > 0 ? ((totalErrors / Math.max(totalCalls, 1)) * 100).toFixed(2) : 0}%`} valueStyle={{ color: totalErrors > 0 ? '#ff4d4f' : '#52c41a' }} /></Card></Col>
        <Col span={6}><Card size="small" styles={{ body: { padding: '16px 20px' } }}><Statistic title="平均延迟" value={`${avgDuration.toFixed(1)} ms`} valueStyle={{ color: '#722ed1' }} /></Card></Col>
      </Row>
      {data.length === 0 && !loading ? (
        <AppEmpty description={`暂无「${name}」的链路指标数据`} tip="确认服务名拼写正确且链路追踪数据已写入" />
      ) : (
        <>
          <Row gutter={[12, 12]}>
            <Col xs={24} md={12}><Card size="small" title="每分钟调用量" styles={{ body: { background: '#121826' } }}><ReactECharts option={callsOption} style={{ height: 240 }} theme="dark" /></Card></Col>
            <Col xs={24} md={12}><Card size="small" title="每分钟错误数" styles={{ body: { background: '#121826' } }}><ReactECharts option={errorOption} style={{ height: 240 }} theme="dark" /></Card></Col>
          </Row>
          <Row gutter={[12, 12]} style={{ marginTop: 12 }}>
            <Col span={24}><Card size="small" title="平均延迟趋势" styles={{ body: { background: '#121826' } }}><ReactECharts option={durationOption} style={{ height: 240 }} theme="dark" /></Card></Col>
          </Row>
        </>
      )}
    </Spin>
  )
}

export default ServiceDetail
