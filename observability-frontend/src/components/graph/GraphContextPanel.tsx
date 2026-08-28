import React from 'react'
import { Alert, Card, Descriptions, Empty, Space, Tag } from 'antd'

type GraphContextPanelProps = {
  context: Record<string, unknown> | null
}

function text(value: unknown): string {
  return value === undefined || value === null || value === '' ? '—' : String(value)
}

function count(value: unknown): number {
  return Array.isArray(value) ? value.length : 0
}

export default function GraphContextPanel({ context }: GraphContextPanelProps) {
  if (!context) {
    return <Card title="RCA Graph Context" size="small"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无持久化 Graph Context" /></Card>
  }
  const partial = context.partial === true
  const stale = context.stale === true
  const warnings = Array.isArray(context.warning_codes) ? context.warning_codes.map(String) : []
  return (
    <Card title="RCA Graph Context" size="small" extra={<Space>
      <Tag color={partial ? 'orange' : 'green'}>{partial ? '部分结果' : '完整结果'}</Tag>
      {stale && <Tag color="gold">数据陈旧</Tag>}
    </Space>}>
      <Descriptions size="small" column={3}>
        <Descriptions.Item label="Context 版本">{text(context.context_version)}</Descriptions.Item>
        <Descriptions.Item label="Graph Schema">{text(context.graph_schema_version ?? context.schema_version)}</Descriptions.Item>
        <Descriptions.Item label="Generation">{text(context.graph_generation)}</Descriptions.Item>
        <Descriptions.Item label="快照时间">{text(context.snapshot_at)}</Descriptions.Item>
        <Descriptions.Item label="窗口起点">{text(context.window_start)}</Descriptions.Item>
        <Descriptions.Item label="窗口终点">{text(context.window_end)}</Descriptions.Item>
        <Descriptions.Item label="实体数">{count(context.vertices)}</Descriptions.Item>
        <Descriptions.Item label="关系数">{count(context.edges)}</Descriptions.Item>
        <Descriptions.Item label="传播路径数">{count(context.propagation_paths)}</Descriptions.Item>
      </Descriptions>
      {warnings.length > 0 && <Alert type="warning" showIcon message={`Graph Context 警告：${warnings.join('、')}`} />}
    </Card>
  )
}
