import React, { useEffect, useState } from 'react'
import { Alert, Card, Empty, Input, Spin } from 'antd'
import { getGraphHealth, getGraphNeighbors, searchGraphEntities } from '../../api/knowledgeGraph'
import type { GraphHealth, GraphSubgraph } from '../../api/graphContracts'
import GraphExplorer from '../../components/graph/GraphExplorer'
import GraphSummary from '../../components/graph/GraphSummary'

export default function ResourceRelationships() {
  const [query, setQuery] = useState('order')
  const [subgraph, setSubgraph] = useState<GraphSubgraph>()
  const [health, setHealth] = useState<GraphHealth>()
  const [loading, setLoading] = useState(false)
  const load = async () => { if (query.trim().length < 2) return; setLoading(true); try { const [h, found] = await Promise.all([getGraphHealth(), searchGraphEntities({ q: query.trim(), entity_type: 'service', limit: 20 })]); setHealth(h.data); const first = found.data.items?.[0]; if (first) setSubgraph((await getGraphNeighbors(first.entity_uid, { depth: 2 })).data) } catch (error: any) { setHealth(undefined); setSubgraph(undefined) } finally { setLoading(false) } }
  useEffect(() => { void load() }, [])
  return <div><h2>资源关系</h2><Input.Search value={query} onChange={(e) => setQuery(e.target.value)} onSearch={() => void load()} enterButton="探索" /><div style={{ margin: '16px 0' }}><GraphSummary subgraph={subgraph} health={health} /></div>{loading ? <Spin /> : subgraph ? <GraphExplorer subgraph={subgraph} /> : health?.ready === false ? <Alert type="warning" message="图谱暂不可用，普通观测查询仍可继续。" /> : <Empty description="输入服务名查找关系" />}</div>
}
