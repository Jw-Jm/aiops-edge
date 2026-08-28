import React from 'react'
import { Card, Empty, List, Space, Tag, Typography } from 'antd'
import type { PanoramaGroupEdge, ServiceMapResponse } from '../../api/knowledgeGraph'

function healthColor(health: string): string {
  if (health === 'healthy') return 'green'
  if (health === 'degraded') return 'orange'
  if (health === 'critical') return 'red'
  return 'default'
}

function formatRate(value: number): string {
  return `${(Number(value || 0) * 100).toFixed(1)}%`
}

function GroupEdge({ edge, onSelect }: { edge: PanoramaGroupEdge; onSelect?: (edge: PanoramaGroupEdge) => void }) {
  return <button type="button" onClick={() => onSelect?.(edge)} style={{ border: 0, background: 'transparent', padding: 0, textAlign: 'left', cursor: onSelect ? 'pointer' : 'default' }}>
    <Space size={6} wrap>
      <Typography.Text strong>{edge.source_name}</Typography.Text>
      <span aria-hidden="true">→</span>
      <Typography.Text strong>{edge.target_name}</Typography.Text>
      <Tag>{edge.routes} routes</Tag>
      <Tag color={edge.error_rate > 0.03 ? 'red' : 'blue'}>{edge.calls.toLocaleString()} calls</Tag>
      {edge.error_rate > 0 ? <Tag color="red">{formatRate(edge.error_rate)}</Tag> : null}
      {edge.latency_ms > 0 ? <Tag>{edge.latency_ms.toFixed(0)}ms</Tag> : null}
    </Space>
  </button>
}

/**
 * Grouped service map used by the default panorama.  It deliberately renders
 * application/namespace cards and aggregated cross-group routes; raw
 * service-to-service edges belong to the explicit expert explorer only.
 */
export default function ServiceMapView({ data, selectedService, onServiceSelect, onGroupEdgeSelect }: {
  data?: ServiceMapResponse
  selectedService?: string
  onServiceSelect?: (serviceName: string) => void
  onGroupEdgeSelect?: (edge: PanoramaGroupEdge) => void
}) {
  if (!data || data.groups.length === 0) return <Empty description="暂无服务地图数据" />
  return <section aria-label="服务地图" data-testid="service-map-view">
    <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <Typography.Text type="secondary">按 {data.group_by === 'application' ? 'Application' : 'Namespace'} 聚合；跨组关系按 routes/calls 汇总。</Typography.Text>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 12 }}>
        {data.groups.map((group) => <Card key={group.group_uid} size="small" title={<Space><span>{group.name}</span><Tag>{group.service_count} services</Tag></Space>} style={{ borderColor: group.services.some((service) => service.service_name === selectedService) ? 'var(--primary)' : undefined }}>
          <Space size={4} wrap style={{ marginBottom: 8 }}>
            <Tag color="green">正常 {group.healthy}</Tag>
            <Tag color="orange">降级 {group.degraded}</Tag>
            <Tag color="red">严重 {group.critical}</Tag>
            <Tag>{group.calls.toLocaleString()} calls</Tag>
          </Space>
          <List size="small" dataSource={group.services} renderItem={(service) => <List.Item>
            <button type="button" onClick={() => onServiceSelect?.(service.service_name)} style={{ border: 0, background: 'transparent', padding: 0, cursor: onServiceSelect ? 'pointer' : 'default', textAlign: 'left' }}>
              <Space size={6} wrap><Typography.Text strong={service.service_name === selectedService}>{service.service_name}</Typography.Text><Tag color={healthColor(service.health)}>{service.health}</Tag>{service.namespace ? <Typography.Text type="secondary">{service.namespace}</Typography.Text> : null}{service.error_rate > 0 ? <Tag color={service.error_rate > 0.03 ? 'red' : 'orange'}>{formatRate(service.error_rate)}</Tag> : null}{service.avg_latency_ms > 0 ? <Tag>{service.avg_latency_ms.toFixed(0)}ms</Tag> : null}</Space>
            </button>
          </List.Item>} />
        </Card>)}
      </div>
      {data.aggregated_edges.length ? <Card size="small" title="跨分组调用" extra={<Tag>{data.aggregated_edges.length} aggregated routes</Tag>}>
        <List size="small" dataSource={data.aggregated_edges} renderItem={(edge) => <List.Item><GroupEdge edge={edge} onSelect={onGroupEdgeSelect} /></List.Item>} />
      </Card> : null}
    </Space>
  </section>
}
