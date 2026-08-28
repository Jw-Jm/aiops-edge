import React from 'react'
import { Button, Card, Input, Table } from 'antd'
import type { PanoramaService } from '../../../api/knowledgeGraph'

export type ServiceListRow = PanoramaService & { service: string }

export default function ServiceListView({ services, selectedService, search, health, onlyAbnormal, onSearchChange, onSelect }: {
  services: ServiceListRow[]
  selectedService: string
  search: string
  health: string
  onlyAbnormal: boolean
  onSearchChange: (value: string) => void
  onSelect: (row: ServiceListRow) => void
}) {
  const visible = services
    .filter((row) => row.service.toLowerCase().includes(search.toLowerCase()))
    .filter((row) => !health || row.health === health)
    .filter((row) => !onlyAbnormal || ['degraded', 'critical'].includes(row.health))
  return <Card title="服务列表" extra={<Input allowClear placeholder="筛选服务" value={search} onChange={(event) => onSearchChange(event.target.value)} style={{ width: 220 }} />}>
    <Table<ServiceListRow> rowKey={(row) => row.entity_uid || row.service} size="small" dataSource={visible} pagination={{ pageSize: 20, showSizeChanger: false }} rowClassName={(row) => row.service === selectedService ? 'service-row-selected' : ''} columns={[
      { title: '服务', dataIndex: 'service', render: (value: string, row) => <Button type="link" onClick={() => onSelect(row)}>{value}</Button> },
      { title: '调用量', dataIndex: 'calls', sorter: (left, right) => left.calls - right.calls },
      { title: '错误数', dataIndex: 'errors', sorter: (left, right) => left.errors - right.errors },
      { title: '错误率', dataIndex: 'error_rate', render: (value: number) => `${(value * 100).toFixed(1)}%` },
      { title: '平均延迟', dataIndex: 'avg_latency_ms', render: (value: number) => `${value.toFixed(0)}ms` },
    ]} locale={{ emptyText: '暂无服务指标' }} />
  </Card>
}
