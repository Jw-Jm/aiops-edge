import React from 'react'
import { Card, Col, List, Row, Space, Statistic, Tag, Typography } from 'antd'
import type { PanoramaEdge, PanoramaService, ServiceOverviewResponse } from '../../../api/knowledgeGraph'
import type { GraphHealth } from '../../../api/graphContracts'

function rate(value: number): string { return `${(Number(value || 0) * 100).toFixed(1)}%` }

function ServiceItem({ service }: { service: PanoramaService }) {
  return <Space size={6} wrap><Typography.Text>{service.service_name}</Typography.Text><Tag color={service.health === 'critical' ? 'red' : service.health === 'degraded' ? 'orange' : 'default'}>{service.health}</Tag><Tag>{rate(service.error_rate)}</Tag><Typography.Text type="secondary">{service.calls.toLocaleString()} calls</Typography.Text></Space>
}

function EdgeItem({ edge, latency }: { edge: PanoramaEdge; latency?: boolean }) {
  return <Space size={6} wrap><Typography.Text>{edge.source_service} → {edge.target_service}</Typography.Text><Tag color={edge.error_rate > 0.03 ? 'red' : 'blue'}>{latency ? `${edge.latency_ms.toFixed(0)}ms` : rate(edge.error_rate)}</Tag><Typography.Text type="secondary">{edge.calls.toLocaleString()} calls</Typography.Text></Space>
}

export default function ServiceSummary({ overview, services, health, timeRange, loading }: {
  overview?: ServiceOverviewResponse
  services: PanoramaService[]
  health?: GraphHealth
  timeRange: number
  loading?: boolean
}) {
  const abnormal = overview?.top_abnormal_services || []
  const errors = overview?.top_error_edges || []
  const latency = overview?.top_latency_edges || []
  const fallback = services[0]
  return <Card title="服务摘要" loading={loading}>
    <Row gutter={16}>
      <Col xs={12} md={3}><Statistic title="服务总数" value={overview?.total ?? services.length} /></Col>
      <Col xs={12} md={3}><Statistic title="健康" value={overview?.healthy ?? services.filter((s) => s.health === 'healthy').length} /></Col>
      <Col xs={12} md={3}><Statistic title="异常" value={(overview?.degraded ?? 0) + (overview?.critical ?? 0)} /></Col>
      <Col xs={12} md={3}><Statistic title="调用量" value={overview?.calls ?? fallback?.calls ?? 0} /></Col>
      <Col xs={12} md={3}><Statistic title="错误率" value={rate(overview?.error_rate ?? fallback?.error_rate ?? 0)} /></Col>
      <Col xs={12} md={3}><Statistic title="平均延迟" value={`${(overview?.avg_latency_ms ?? fallback?.avg_latency_ms ?? 0).toFixed(0)}ms`} /></Col>
      <Col xs={12} md={3}><Statistic title="P95 延迟" value={`${(overview?.p95_latency_ms ?? 0).toFixed(0)}ms`} /></Col>
      <Col xs={12} md={3}><Statistic title="跨 namespace" value={overview?.cross_namespace_edges ?? 0} /></Col>
      <Col xs={12} md={3}><Statistic title="循环依赖" value={overview?.cycle_count ?? 0} /></Col>
    </Row>
    <div style={{ marginTop: 12 }}><Tag color={health?.ready ? 'green' : 'red'}>图谱：{health?.ready ? `${health.backend} 就绪` : '不可用'}</Tag><Tag>窗口：近 {timeRange} 分钟</Tag></div>
    <Row gutter={16} style={{ marginTop: 16 }}>
      <Col xs={24} md={8}><Typography.Text strong>异常服务 Top 5</Typography.Text><List size="small" dataSource={abnormal} locale={{ emptyText: '暂无异常服务' }} renderItem={(service) => <List.Item><ServiceItem service={service} /></List.Item>} /></Col>
      <Col xs={24} md={8}><Typography.Text strong>高错误调用 Top 5</Typography.Text><List size="small" dataSource={errors} locale={{ emptyText: '暂无高错误调用' }} renderItem={(edge) => <List.Item><EdgeItem edge={edge} /></List.Item>} /></Col>
      <Col xs={24} md={8}><Typography.Text strong>高延迟调用 Top 5</Typography.Text><List size="small" dataSource={latency} locale={{ emptyText: '暂无高延迟调用' }} renderItem={(edge) => <List.Item><EdgeItem edge={edge} latency /></List.Item>} /></Col>
    </Row>
  </Card>
}
