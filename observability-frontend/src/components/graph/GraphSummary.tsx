import React from 'react'
import { Card, Col, Row, Statistic, Tag } from 'antd'
import type { GraphHealth, GraphSubgraph } from '../../api/graphContracts'

export default function GraphSummary({ subgraph, health }: { subgraph?: GraphSubgraph; health?: GraphHealth }) {
  const meta = subgraph?.meta
  return <Card size="small" title="知识图谱摘要" data-testid="graph-summary">
    <Row gutter={16}>
      <Col span={6}><Statistic title="实体" value={subgraph?.vertices.length ?? 0} /></Col>
      <Col span={6}><Statistic title="关系" value={subgraph?.edges.length ?? 0} /></Col>
      <Col span={6}><Statistic title="后端" value={health?.backend ?? '—'} /></Col>
      <Col span={6}><Tag color={health?.ready ? 'green' : 'red'}>{health?.ready ? '就绪' : '不可用'}</Tag>{meta?.partial && <Tag color="orange">部分结果</Tag>}{meta?.stale && <Tag color="gold">数据陈旧</Tag>}</Col>
    </Row>
    {meta?.warning_codes?.length ? <div style={{ marginTop: 10, color: 'var(--warning)' }}>警告：{meta.warning_codes.join('、')}</div> : null}
  </Card>
}
