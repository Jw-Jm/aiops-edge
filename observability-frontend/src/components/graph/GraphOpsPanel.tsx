import React, { useEffect, useState } from 'react'
import { Card, Tabs, Tag } from 'antd'
import { getGraphOpsAliases, getGraphOpsOutbox, getGraphOpsShadowDiff, getGraphOpsSyncStates } from '../../api/knowledgeGraph'

export default function GraphOpsPanel() {
  const [data, setData] = useState<Record<string, unknown[]>>({})
  useEffect(() => { Promise.all([getGraphOpsSyncStates(), getGraphOpsOutbox(), getGraphOpsAliases(), getGraphOpsShadowDiff()]).then(([a, b, c, d]) => setData({ sync: a.data.items || [], outbox: b.data.items || [], aliases: c.data.items || [], shadow: d.data.items || [] })).catch(() => setData({})) }, [])
  return <Card title="Graph Ops" extra={<Tag color="blue">只读审计视图</Tag>}><Tabs items={Object.entries(data).map(([key, value]) => ({ key, label: `${key} (${value.length})`, children: <pre style={{ maxHeight: 360, overflow: 'auto' }}>{JSON.stringify(value, null, 2)}</pre> }))} /></Card>
}
