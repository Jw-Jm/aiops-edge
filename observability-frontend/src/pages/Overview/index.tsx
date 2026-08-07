import React, { useEffect, useState } from 'react'
import { Row, Col, Card, Statistic, Tag, Space, Progress, Typography, Button } from 'antd'
import {
  DatabaseOutlined, AlertOutlined, FileSearchOutlined,
  ApartmentOutlined, NodeIndexOutlined, ThunderboltOutlined, ArrowRightOutlined,
  RobotOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import api from '../../api/client'

const { Text } = Typography

const Overview: React.FC = () => {
  const navigate = useNavigate()
  const [stats, setStats] = useState({
    services: 0, alerts: 0, edges: 0, errorRate: 0, avgLatency: 0,
  })
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const load = async () => {
      try {
        // 优先走 /dashboard/stats 聚合接口；失败则回退到原 3 个并发请求兜底
        try {
          const res = await api.get('/dashboard/stats')
          const d = res?.data
          setStats({
            services: d?.services ?? 0,
            alerts: 0,
            edges: d?.edges ?? 0,
            errorRate: d?.error_rate ?? 0,
            avgLatency: d?.avg_latency_ms ?? 0,
          })
        } catch {
          const [svcRes, alertRes, topoRes] = await Promise.allSettled([
            api.get('/services'),
            api.get('/alerts/events', { params: { limit: 1 } }),
            api.get('/topology/global'),
          ])
          const services = (() => {
            if (svcRes.status !== 'fulfilled') return 0
            const d = svcRes.value?.data
            if (Array.isArray(d)) return d.length
            if (Array.isArray(d?.data)) return d.data.length
            if (typeof d?.count === 'number') return d.count
            return 0
          })()
          const alerts = (() => {
            if (alertRes.status !== 'fulfilled') return 0
            const d = alertRes.value?.data
            if (typeof d?.total === 'number') return d.total
            if (typeof d?.count === 'number') return d.count
            if (Array.isArray(d?.data)) return d.data.length
            if (Array.isArray(d)) return d.length
            return 0
          })()
          const edges = (() => {
            if (topoRes.status !== 'fulfilled') return 0
            const d = topoRes.value?.data
            if (typeof d?.edge_count === 'number') return d.edge_count
            if (Array.isArray(d?.edges)) return d.edges.length
            if (typeof d?.edges === 'number') return d.edges
            return 0
          })()
          setStats({ services, alerts, edges, errorRate: 0, avgLatency: 0 })
        }
      } catch { /* ignore */ } finally {
        setLoading(false)
      }
    }
    load()
  }, [])

  const statCards = [
    { title: '服务数量', value: stats.services, icon: <DatabaseOutlined />, color: '#1677ff', path: '/services', desc: '已观测服务' },
    { title: '拓扑调用', value: stats.edges, icon: <ApartmentOutlined />, color: '#722ed1', path: '/topology', desc: '服务调用关系' },
    { title: '错误率', value: `${stats.errorRate.toFixed(2)}%`, icon: <AlertOutlined />, color: '#fa8c16', path: '/alerts', desc: '近 24h 请求错误率' },
    { title: '平均延迟', value: `${stats.avgLatency.toFixed(1)}ms`, icon: <ThunderboltOutlined />, color: '#52c41a', path: '/traces', desc: '近 24h 平均调用延迟' },
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
            <div style={{ fontSize: 13, opacity: 0.9 }}>全栈可观测 · AI 诊断 · 智能告警</div>
          </div>
        </Space>
        <Space style={{ position: 'absolute', right: 24, bottom: 20 }}>
          <Button ghost icon={<ArrowRightOutlined />} onClick={() => navigate('/aichat')}>进入 AI 诊断</Button>
        </Space>
      </div>

      {/* 关键指标 */}
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

      {/* 功能入口 */}
      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        {[
          { title: '服务拓扑', desc: '查看服务调用关系', icon: <ApartmentOutlined />, color: '#1677ff', path: '/topology' },
          { title: '链路追踪', desc: '分析请求调用链', icon: <NodeIndexOutlined />, color: '#722ed1', path: '/traces' },
          { title: '日志查询', desc: '检索平台日志', icon: <FileSearchOutlined />, color: '#52c41a', path: '/logs' },
          { title: 'AI 诊断', desc: '智能根因分析', icon: <RobotOutlined />, color: '#13c2c2', path: '/aichat' },
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
