import React, { useState } from 'react'
import { Button, Card, Space, Tag, Typography } from 'antd'
import type { PlatformResourceRef } from '../../features/resources/types'
import DataState from '../../components/display/DataState'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'

export const MainFailureChain: React.FC<{ center?: PlatformResourceRef; upstream?: PlatformResourceRef[]; downstream?: PlatformResourceRef[] }> = ({ center, upstream = [], downstream = [] }) => {
  const [expanded, setExpanded] = useState(false)
  const nodes = [...(expanded ? upstream : upstream.slice(0, 1)), center, ...(expanded ? downstream : downstream.slice(0, 1))].filter(Boolean) as PlatformResourceRef[]
  const hasMore = upstream.length > 1 || downstream.length > 1
  return <Card title="主故障链" size="small" extra={<Space><Tag>默认仅显示直接邻居</Tag>{hasMore && <Button type="link" size="small" onClick={() => setExpanded((value) => !value)}>{expanded ? '收起上下游' : '展开上下游'}</Button>}</Space>}><Typography.Text type="secondary">关系链优先展示与当前异常直接相关的持久化依赖；完整图谱请进入专家关系探索。</Typography.Text>{nodes.length ? <Space wrap style={{ marginTop: 12 }}>{nodes.map((node, index) => <React.Fragment key={`${node.type}-${node.uid}`}><Tag data-testid="main-chain-node">{resourceTypeLabel(node.type)} · {resourceLocation(node)}</Tag>{index < nodes.length - 1 && <span aria-hidden="true">→</span>}</React.Fragment>)}</Space> : <DataState kind="empty" compact title="选择资源后显示主故障链" />}</Card>
}

export default MainFailureChain
