import React, { useMemo } from 'react'
import { Button, Empty, Select, Space, Table } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { GraphEdge, GraphEntity } from '../../api/graphContracts'
import type { PanoramaService, ServiceMatrixCell, ServiceMatrixResponse } from '../../api/knowledgeGraph'

type MatrixRow = { key: string; source: string; [target: string]: React.ReactNode }
type MatrixMetric = 'combined' | 'calls' | 'errors' | 'error_rate' | 'latency_ms'
const SERVICE_TYPES = new Set(['service', 'application', 'middleware', 'k8s_service'])

function cellMetric(cell: ServiceMatrixCell, mode: MatrixMetric): React.ReactNode {
  if (mode === 'calls') return cell.calls.toLocaleString()
  if (mode === 'errors') return cell.errors.toLocaleString()
  if (mode === 'error_rate') return `${(cell.error_rate * 100).toFixed(1)}%`
  if (mode === 'latency_ms') return `${cell.latency_ms.toFixed(0)}ms`
  return <span title={`${cell.source_service} → ${cell.target_service}`}>
    <span>{cell.calls.toLocaleString()}</span> · <span>{(cell.error_rate * 100).toFixed(1)}%</span> · <span>{cell.latency_ms.toFixed(0)}ms</span>
  </span>
}

function graphCell(edge: GraphEdge): ServiceMatrixCell {
  const attrs = edge.attrs ?? {}
  const calls = Number(attrs.calls ?? attrs.request_count ?? attrs.call_count ?? 0)
  const errors = Number(attrs.errors ?? attrs.error_count ?? 0)
  const rawRate = Number(attrs.error_rate ?? attrs.errorRate ?? (calls ? errors / calls : 0))
  return { source_uid: edge.source_uid, target_uid: edge.target_uid, source_service: edge.source_uid, target_service: edge.target_uid, calls, errors, error_rate: rawRate > 1 ? rawRate / 100 : rawRate, latency_ms: Number(attrs.latency_ms ?? attrs.avg_latency_ms ?? attrs.avg_ms ?? 0), cross_namespace: false }
}

function serviceKey(service: PanoramaService): string {
  return `${service.application_name ?? ''}\u0000${service.namespace ?? ''}\u0000${service.service_name}\u0000${service.entity_uid ?? ''}`
}

export default function CallMatrix({ matrix, vertices = [], edges = [], onCellSelect }: {
  matrix?: ServiceMatrixResponse
  vertices?: GraphEntity[]
  edges?: GraphEdge[]
  onCellSelect?: (cell: ServiceMatrixCell) => void
}) {
  const [metricMode, setMetricMode] = React.useState<MatrixMetric>('combined')
  const services = useMemo(() => {
    const source = matrix?.services ?? vertices.filter((vertex) => SERVICE_TYPES.has(vertex.entity_type)).map((vertex) => ({
      entity_uid: vertex.entity_uid, service_name: vertex.name || vertex.entity_uid, namespace: vertex.namespace,
      application_name: String(vertex.attrs?.application_name ?? vertex.attrs?.application ?? ''), health: vertex.health ?? 'unknown', calls: 0, errors: 0, error_rate: 0, avg_latency_ms: 0,
    } satisfies PanoramaService))
    return source.slice().sort((left, right) => serviceKey(left).localeCompare(serviceKey(right))).slice(0, 200)
  }, [matrix, vertices])
  const serviceIds = useMemo(() => new Set(services.map((service) => service.entity_uid || `service:name:${service.service_name}`)), [services])
  const byPair = useMemo(() => {
    const result = new Map<string, ServiceMatrixCell>()
    if (matrix) {
      for (const cell of matrix.cells) result.set(`${cell.source_uid}→${cell.target_uid}`, cell)
      return result
    }
    for (const edge of edges) {
      if (!serviceIds.has(edge.source_uid) || !serviceIds.has(edge.target_uid)) continue
      const cell = graphCell(edge)
      result.set(`${cell.source_uid}→${cell.target_uid}`, cell)
    }
    return result
  }, [edges, matrix, serviceIds])
  const columns = useMemo<ColumnsType<MatrixRow>>(() => [
    { title: '调用方 / 被调用方', dataIndex: 'source', key: 'source', fixed: 'left', width: 180 },
    ...services.map((target) => ({ title: target.service_name, dataIndex: target.entity_uid || `service:name:${target.service_name}`, key: target.entity_uid || target.service_name, width: 150 })),
  ], [services])
  const data = useMemo<MatrixRow[]>(() => services.map((source) => {
    const sourceUID = source.entity_uid || `service:name:${source.service_name}`
    const row: MatrixRow = { key: sourceUID, source: source.service_name }
    for (const target of services) {
      const targetUID = target.entity_uid || `service:name:${target.service_name}`
      const cell = byPair.get(`${sourceUID}→${targetUID}`)
      if (sourceUID === targetUID) row[targetUID] = '—'
      else if (!cell) row[targetUID] = '·'
      else row[targetUID] = <Button type="link" size="small" aria-label={`${source.service_name} 调用 ${target.service_name}`} onClick={() => onCellSelect?.(cell)}>{cellMetric(cell, metricMode)}</Button>
    }
    return row
  }), [byPair, metricMode, onCellSelect, services])

  if (!services.length) return <section aria-label="调用矩阵"><h3>调用矩阵</h3><Empty description="暂无服务调用关系" /></section>
  return <section aria-label="调用矩阵" data-testid="call-matrix">
    <Space style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
      <h3 style={{ margin: 0 }}>调用矩阵</h3>
      <Select aria-label="矩阵指标" size="small" value={metricMode} onChange={setMetricMode} options={[
        { value: 'combined', label: '调用量 / 错误率 / 延迟' }, { value: 'calls', label: '调用量' }, { value: 'errors', label: '错误数' }, { value: 'error_rate', label: '错误率' }, { value: 'latency_ms', label: '平均延迟' },
      ]} />
    </Space>
    <Table<MatrixRow> size="small" bordered pagination={{ pageSize: 30, showSizeChanger: false }} scroll={{ x: Math.max(480, 180 + services.length * 150), y: 480 }} columns={columns} dataSource={data} locale={{ emptyText: '暂无服务调用关系' }} />
  </section>
}
