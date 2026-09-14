import React, { useEffect, useState } from 'react'
import { Card, Tabs, Tag, Empty } from 'antd'
import { getGraphOpsAliases, getGraphOpsOutbox, getGraphOpsShadowDiff, getGraphOpsSyncStates } from '../../api/knowledgeGraph'
import RawDataPanel from '../display/RawDataPanel'

// PF-PAGE-025: 四类数据源（sync/outbox/aliases/shadow）独立拉取，
// 任一接口（如 /ai/kg/ops/aliases 503）失败只影响对应 Tab，
// 不再因 Promise.all 整体 reject 导致面板空白。
export default function GraphOpsPanel() {
  const [data, setData] = useState<Record<string, unknown[]>>({})
  const [failed, setFailed] = useState<Record<string, boolean>>({})
  useEffect(() => {
    Promise.allSettled([getGraphOpsSyncStates(), getGraphOpsOutbox(), getGraphOpsAliases(), getGraphOpsShadowDiff()]).then((rs) => {
      const next: Record<string, unknown[]> = {}
      const bad: Record<string, boolean> = {}
      const keys = ['sync', 'outbox', 'aliases', 'shadow']
      rs.forEach((r, i) => {
        const key = keys[i]
        if (r.status === 'fulfilled') {
          next[key] = (r.value.data as any)?.items || []
          bad[key] = false
        } else {
          next[key] = []
          bad[key] = true
        }
      })
      setData(next)
      setFailed(bad)
    })
  }, [])
  return (
    <Card title="Graph Ops" extra={<Tag color="blue">只读审计视图</Tag>}>
      <Tabs items={Object.entries(data).map(([key, value]) => ({
        key,
        label: `${key} (${value.length})`,
        children: failed[key]
          ? <Empty description="数据不可用" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: 40 }} />
          : <RawDataPanel title="查看原始数据" data={value} />,
      }))} />
    </Card>
  )
}
