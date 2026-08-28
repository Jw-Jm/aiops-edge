import React, { useMemo } from 'react'
import { Table } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { GraphEdge, GraphEntity } from '../../api/graphContracts'

type MatrixRow = { key: string; source: string; [target: string]: React.ReactNode }
const SERVICE_TYPES = new Set(['service', 'application', 'middleware', 'k8s_service'])

function metric(edge: GraphEdge) {
  const attrs = edge.attrs ?? {}
  const calls = Number(attrs.calls ?? attrs.request_count ?? attrs.call_count ?? 0)
  const errorRate = attrs.error_rate ?? attrs.errorRate
  const latency = Number(attrs.latency_ms ?? attrs.avg_latency_ms ?? attrs.avg_ms ?? 0)
  const suffix: string[] = []
  if (errorRate !== undefined && errorRate !== null) {
    const raw = Number(errorRate)
    suffix.push(`${(raw > 1 ? raw : raw * 100).toFixed(1)}%`)
  }
  if (latency > 0) suffix.push(`${latency.toFixed(0)}ms`)
  return <span title={edge.relation_type}><span>{calls.toLocaleString()}</span>{suffix.length ? <> · {suffix.map((value) => <span key={value}>{value}</span>)}</> : null}</span>
}

export default function CallMatrix({ vertices, edges }: { vertices: GraphEntity[]; edges: GraphEdge[] }) {
  const services = useMemo(() => vertices.filter((vertex) => SERVICE_TYPES.has(vertex.entity_type)).slice(0, 300), [vertices])
  const serviceIds = useMemo(() => new Set(services.map((service) => service.entity_uid)), [services])
  const edgeByPair = useMemo(() => {
    const result = new Map<string, GraphEdge>()
    for (const edge of edges) {
      if (!serviceIds.has(edge.source_uid) || !serviceIds.has(edge.target_uid)) continue
      const key = `${edge.source_uid}→${edge.target_uid}`
      const previous = result.get(key)
      if (!previous) {
        result.set(key, { ...edge, attrs: { ...(edge.attrs || {}) } })
        continue
      }
      const previousAttrs = previous.attrs || {}
      const currentAttrs = edge.attrs || {}
      const previousCalls = Number(previousAttrs.calls ?? previousAttrs.request_count ?? previousAttrs.call_count ?? 0)
      const currentCalls = Number(currentAttrs.calls ?? currentAttrs.request_count ?? currentAttrs.call_count ?? 0)
      const totalCalls = previousCalls + currentCalls
      const rate = (attrs: Record<string, unknown>) => {
        const value = attrs.error_rate ?? attrs.errorRate
        if (value === undefined || value === null) return 0
        const numeric = Number(value)
        return numeric > 1 ? numeric / 100 : numeric
      }
      const latency = (attrs: Record<string, unknown>) => Number(attrs.latency_ms ?? attrs.avg_latency_ms ?? attrs.avg_ms ?? 0)
      const totalWeight = totalCalls || 1
      previous.attrs = {
        ...previousAttrs,
        calls: totalCalls,
        error_rate: (rate(previousAttrs) * previousCalls + rate(currentAttrs) * currentCalls) / totalWeight,
        latency_ms: (latency(previousAttrs) * previousCalls + latency(currentAttrs) * currentCalls) / totalWeight,
      }
      if (previous.relation_type !== edge.relation_type) previous.relation_type = `${previous.relation_type},${edge.relation_type}`
    }
    return result
  }, [edges, serviceIds])
  const columns = useMemo<ColumnsType<MatrixRow>>(() => [
    { title: '调用方 / 被调用方', dataIndex: 'source', key: 'source', fixed: 'left', width: 180 },
    ...services.map((target) => ({ title: target.name || target.entity_uid, dataIndex: target.entity_uid, key: target.entity_uid, width: 150 })),
  ], [services])
  const data = useMemo<MatrixRow[]>(() => services.map((source) => {
    const row: MatrixRow = { key: source.entity_uid, source: source.name || source.entity_uid }
    for (const target of services) {
      const edge = edgeByPair.get(`${source.entity_uid}→${target.entity_uid}`)
      row[target.entity_uid] = source.entity_uid === target.entity_uid ? '—' : edge ? metric(edge) : '·'
    }
    return row
  }), [edgeByPair, services])

  return <section aria-label="调用矩阵">
    <h3>调用矩阵</h3>
    <Table<MatrixRow> size="small" bordered pagination={{ pageSize: 30, showSizeChanger: false }} scroll={{ x: Math.max(480, 180 + services.length * 150), y: 480 }} columns={columns} dataSource={data} locale={{ emptyText: '暂无服务调用关系' }} />
  </section>
}
