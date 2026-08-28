import React from 'react'
import { Card, Empty, Space, Tag, Typography } from 'antd'
import type { ServiceDependenciesResponse } from '../../api/knowledgeGraph'
import type { GraphEntity } from '../../api/graphContracts'

function EntityChip({ entity, center, onSelect }: { entity: GraphEntity; center?: boolean; onSelect?: (entity: GraphEntity) => void }) {
  return <button type="button" onClick={() => onSelect?.(entity)} style={{ border: center ? '1px solid var(--primary)' : '1px solid var(--border-soft)', background: center ? 'var(--bg-soft)' : 'var(--bg)', borderRadius: 8, padding: '8px 10px', cursor: onSelect ? 'pointer' : 'default', textAlign: 'left' }}>
    <Typography.Text strong={center}>{entity.name || entity.entity_uid}</Typography.Text>
    <br /><Typography.Text type="secondary">{entity.entity_type}{entity.namespace ? ` · ${entity.namespace}` : ''}</Typography.Text>
  </button>
}

/** Stable upstream → selected → downstream dependency lanes. */
export default function ServiceDependencyView({ data, onEntitySelect }: { data?: ServiceDependenciesResponse; onEntitySelect?: (entity: GraphEntity) => void }) {
  if (!data) return <Card title="依赖主链"><Empty description="请选择服务查看依赖主链" /></Card>
  return <Card title="依赖主链" extra={<Space><Tag color="blue">拓扑 revision {data.topology_revision || '—'}</Tag>{data.meta.stale ? <Tag color="gold">数据陈旧</Tag> : null}</Space>}>
    <div style={{ display: 'grid', gridTemplateColumns: '1fr minmax(180px, 240px) 1fr', gap: 16, alignItems: 'start' }}>
      <section aria-label="上游调用方"><Typography.Text type="secondary">上游调用方</Typography.Text><Space direction="vertical" style={{ width: '100%', marginTop: 8 }}>{data.upstream.length ? data.upstream.map((entity) => <EntityChip key={entity.entity_uid} entity={entity} onSelect={onEntitySelect} />) : <Typography.Text type="secondary">无</Typography.Text>}</Space></section>
      <section aria-label="当前服务"><Typography.Text type="secondary">当前服务</Typography.Text><div style={{ marginTop: 8 }}><EntityChip entity={data.center} center onSelect={onEntitySelect} /></div></section>
      <section aria-label="下游依赖"><Typography.Text type="secondary">下游依赖</Typography.Text><Space direction="vertical" style={{ width: '100%', marginTop: 8 }}>{data.downstream.length ? data.downstream.map((entity) => <EntityChip key={entity.entity_uid} entity={entity} onSelect={onEntitySelect} />) : <Typography.Text type="secondary">无</Typography.Text>}</Space></section>
    </div>
    <section aria-label="中间件依赖" style={{ marginTop: 16 }}><Typography.Text type="secondary">中间件泳道</Typography.Text><Space wrap style={{ marginTop: 8 }}>{data.middleware.length ? data.middleware.map((entity) => <EntityChip key={entity.entity_uid} entity={entity} onSelect={onEntitySelect} />) : <Typography.Text type="secondary">无</Typography.Text>}</Space></section>
    {data.cycles.length ? <section aria-label="循环依赖" style={{ marginTop: 16 }}><Typography.Text type="danger">循环依赖</Typography.Text><div>{data.cycles.map((cycle) => <Tag color="orange" key={cycle.join('→')}>{cycle.join(' → ')}</Tag>)}</div></section> : null}
  </Card>
}
