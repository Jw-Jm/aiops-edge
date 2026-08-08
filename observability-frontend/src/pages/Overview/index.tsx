import React, { useEffect, useState, useCallback } from 'react'
import { Row, Col, Card, Statistic, Space, Typography, Button, Select, Empty } from 'antd'
import {
  DatabaseOutlined, AlertOutlined, FileSearchOutlined,
  ApartmentOutlined, NodeIndexOutlined, ThunderboltOutlined, ArrowRightOutlined,
  RobotOutlined, ThunderboltFilled,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import ReactECharts from 'echarts-for-react'
import { getDashboardStats, DashboardStats } from '../../api/client'

const { Text } = Typography

const darkText = '#e8e8e8'
const gridColor = '#1f1f1f'
const tooltipStyle = { background: '#1a1a1a', border: '1px solid #333', textStyle: { color: '#e8e8e8' } }

const Overview: React.FC = () => {
  const navigate = useNavigate()
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [range, setRange] = useState('24h')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getDashboardStats()
      setStats(res.data)
    } catch { /* ignore */ } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  // 趋势折线图（近 24h 调用+错误）
  const trendOption = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', ...tooltipStyle },
    legend: { data: ['调用量', '错误量'], textStyle: { color: darkText }, top: 0 },
    grid: { left: 50, right: 20, top: 30, bottom: 30 },
    xAxis: { type: 'category', data: (stats?.trend || []).map(t => t.t.slice(11, 16)), axisLabel: { color: '#999' }, axisLine: { lineStyle: { color: gridColor } } },
    yAxis: { type: 'value', axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: gridColor } } },
    series: [
      { name: '调用量', type: 'line', smooth: true, data: (stats?.trend || []).map(t => t.calls), itemStyle: { color: '#1677ff' }, areaStyle: { opacity: 0.15 } },
      { name: '错误量', type: 'line', smooth: true, data: (stats?.trend || []).map(t => t.errors), itemStyle: { color: '#ff4d4f' } },
    ],
  }

  // 错误分布柱状图
  const errorsOption = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', ...tooltipStyle },
    grid: { left: 50, right: 20, top: 20, bottom: 30 },
    xAxis: { type: 'value', axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: gridColor } } },
    yAxis: { type: 'category', data: (stats?.top_errors || []).map(e => e.service).reverse(), axisLabel: { color: '#ccc', fontSize: 11 } },
    series: [{ name: '错误数', type: 'bar', data: (stats?.top_errors || []).map(e => e.errors).reverse(), itemStyle: { color: '#ff4d4f', borderRadius: [0, 4, 4, 0] }, barWidth: 14 }],
  }

  // 告警环形图
  const alerts = stats?.alerts || { total: 0, critical: 0, warning: 0, info: 0, by_service: [] }
  const alertOption = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'item', ...tooltipStyle },
    legend: { bottom: 0, textStyle: { color: darkText } },
    series: [{
      type: 'pie', radius: ['45%', '70%'], center: ['50%', '44%'],
      avoidLabelOverlap: false, label: { show: false }, emphasis: { label: { show: true, color: '#fff' } },
      data: [
        { value: alerts.critical, name: '严重', itemStyle: { color: '#ff4d4f' } },
        { value: alerts.warning, name: '警告', itemStyle: { color: '#faad14' } },
        { value: alerts.info, name: '信息', itemStyle: { color: '#1677ff' } },
      ],
    }],
  }

  // TOP 服务调用量条形图
  const topSvcOption = {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', ...tooltipStyle },
    grid: { left: 50, right: 20, top: 20, bottom: 30 },
    xAxis: { type: 'value', axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: gridColor } } },
    yAxis: { type: 'category', data: (stats?.top_services || []).slice(0, 10).map(s => s.service).reverse(), axisLabel: { color: '#ccc', fontSize: 11 } },
    series: [{ name: '调用量', type: 'bar', data: (stats?.top_services || []).slice(0, 10).map(s => s.calls).reverse(), itemStyle: { color: '#1677ff', borderRadius: [0, 4, 4, 0] }, barWidth: 12 }],
  }

  const statCards = [
    { title: '服务数量', value: stats?.services ?? 0, icon: <DatabaseOutlined />, color: '#1677ff', path: '/services', desc: '已观测服务' },
    { title: '拓扑调用', value: stats?.edges ?? 0, icon: <ApartmentOutlined />, color: '#722ed1', path: '/topology', desc: '服务调用关系' },
    { title: '错误率', value: `${(stats?.error_rate ?? 0).toFixed(2)}%`, icon: <AlertOutlined />, color: '#fa8c16', path: '/alerts', desc: '近 24h 请求错误率' },
    { title: 'P95 延迟', value: `${(stats?.latency_p95 ?? 0).toFixed(1)}ms`, icon: <ThunderboltOutlined />, color: '#52c41a', path: '/traces', desc: '近 24h P95 延迟' },
  ]

  return (
    <div>
      {/* 欢迎横幅 */}
      <div style={{ padding: '20px 24px', borderRadius: 12, marginBottom: 16, background: 'linear-gradient(135deg, #1677ff 0%, #722ed1 100%)', color: '#fff', position: 'relative', overflow: 'hidden' }}>
        <div style={{ position: 'absolute', right: -30, top: -30, width: 180, height: 180, borderRadius: '50%', background: 'rgba(255,255,255,0.08)' }} />
        <div style={{ position: 'absolute', right: 60, top: 40, width: 100, height: 100, borderRadius: '50%', background: 'rgba(255,255,255,0.06)' }} />
        <Space size={12}>
          <ThunderboltOutlined style={{ fontSize: 28 }} />
          <div>
            <div style={{ fontSize: 20, fontWeight: 700 }}>AIOps 智能运维平台</div>
            <div style={{ fontSize: 13, opacity: 0.9 }}>全栈可观测 · AI 诊断 · 智能告警 · NL→SQL</div>
          </div>
        </Space>
        <Space style={{ position: 'absolute', right: 24, bottom: 20 }}>
          <Button ghost icon={<ArrowRightOutlined />} onClick={() => navigate('/aichat')}>进入 AI 诊断</Button>
          <Button ghost icon={<ThunderboltFilled />} onClick={() => navigate('/nl2sql')}>SQL 查询</Button>
        </Space>
      </div>

      {/* KPI 卡片 */}
      <Row gutter={[16, 16]}>
        {statCards.map(c => (
          <Col xs={12} md={6} key={c.title}>
            <Card size="small" hoverable onClick={() => navigate(c.path)} style={{ cursor: 'pointer' }}>
              <Space align="center" size={12}>
                <div style={{ width: 40, height: 40, borderRadius: 10, background: c.color + '1a', color: c.color, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 20 }}>{c.icon}</div>
                <div>
                  <Statistic title={c.title} value={c.value} loading={loading} valueStyle={{ fontSize: 24, fontWeight: 700 }} />
                  <Text type="secondary" style={{ fontSize: 11 }}>{c.desc}</Text>
                </div>
              </Space>
            </Card>
          </Col>
        ))}
      </Row>

      {/* 趋势 + 告警 */}
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={16}>
          <Card size="small" title="调用 / 错误趋势" extra={
            <Select size="small" value={range} onChange={setRange}
              options={[{ value: '24h', label: '近 24h' }]} style={{ width: 90 }} />
          }>
            {(stats?.trend || []).length ? <ReactECharts option={trendOption} style={{ height: 280 }} />
              : <Empty description="暂无趋势数据" />}
          </Card>
        </Col>
        <Col xs={24} lg={8}>
          <Card size="small" title={`告警分布（${alerts.total}）`}>
            {alerts.total > 0
              ? <ReactECharts option={alertOption} style={{ height: 280 }} />
              : <Empty description="暂无告警" />}
          </Card>
        </Col>
      </Row>

      {/* 错误分布 + TOP 服务 */}
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={12}>
          <Card size="small" title="TOP 错误服务">
            {(stats?.top_errors || []).length ? <ReactECharts option={errorsOption} style={{ height: 300 }} />
              : <Empty description="暂无错误数据" />}
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card size="small" title="TOP 服务调用量">
            {(stats?.top_services || []).length ? <ReactECharts option={topSvcOption} style={{ height: 300 }} />
              : <Empty description="暂无服务数据" />}
          </Card>
        </Col>
      </Row>

      {/* 功能入口 */}
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        {[
          { title: '服务拓扑', desc: '查看服务调用关系', icon: <ApartmentOutlined />, color: '#1677ff', path: '/topology' },
          { title: '链路追踪', desc: '分析请求调用链', icon: <NodeIndexOutlined />, color: '#722ed1', path: '/traces' },
          { title: '日志查询', desc: '检索平台日志', icon: <FileSearchOutlined />, color: '#52c41a', path: '/logs' },
          { title: 'SQL 查询', desc: '自然语言查 ClickHouse', icon: <ThunderboltFilled />, color: '#13c2c2', path: '/nl2sql' },
          { title: '告警中心', desc: '告警规则与事件', icon: <AlertOutlined />, color: '#ff4d4f', path: '/alerts' },
          { title: '任务工作台', desc: 'AI 诊断任务', icon: <ThunderboltOutlined />, color: '#13c2c2', path: '/tasks' },
        ].map(f => (
          <Col xs={12} md={8} key={f.title}>
            <Card size="small" hoverable onClick={() => navigate(f.path)} style={{ cursor: 'pointer' }}>
              <Space align="center" size={12}>
                <div style={{ width: 36, height: 36, borderRadius: 9, background: f.color + '1a', color: f.color, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 18 }}>{f.icon}</div>
                <div>
                  <div style={{ fontWeight: 600 }}>{f.title}</div>
                  <Text type="secondary" style={{ fontSize: 12 }}>{f.desc}</Text>
                </div>
              </Space>
            </Card>
          </Col>
        ))}
      </Row>
    </div>
  )
}

export default Overview
