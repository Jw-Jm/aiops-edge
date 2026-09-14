import React, { useEffect, useState } from 'react'
import { Button, Card, Input, Select, Space, Tag, Typography } from 'antd'
import { getGraphHealth, getGraphNeighbors, searchGraphEntities } from '../../api/knowledgeGraph'
import type { GraphEntity, GraphHealth, GraphSubgraph } from '../../api/graphContracts'
import GraphExplorer from '../../components/graph/GraphExplorer'
import GraphSummary from '../../components/graph/GraphSummary'
import DataState from '../../components/display/DataState'
import { PageHeader } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import { isDefaultGraphSearchType, resourceDomainOf, resourceTypeLabel } from '../../features/resources/resourceDomain'

// 域过滤只保留真实载体与依赖：业务/应用/APM 语义实体不是默认图谱主语。
const DOMAIN_OPTIONS = [{ value: '', label: '全部资源域' }, { value: 'compute', label: '计算' }, { value: 'network', label: '网络' }, { value: 'storage', label: '存储' }, { value: 'kubernetes', label: 'Kubernetes' }]

function graphRequestErrorMessage(requestError: any): string {
  const error = requestError?.response?.data?.error
  if (typeof error === 'string') return error
  if (error && typeof error === 'object' && typeof error.message === 'string') return error.message
  return requestError?.response?.data?.message || requestError?.message || '关系图读取失败'
}

export default function ResourceRelationships() {
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const [query, setQuery] = useState('')
  const [domain, setDomain] = useState('')
  const [results, setResults] = useState<GraphEntity[]>([])
  const [subgraph, setSubgraph] = useState<GraphSubgraph>()
  const [health, setHealth] = useState<GraphHealth>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = async (selected?: GraphEntity) => {
    if (query.trim().length < 2 || !activeClusterId) return
    setLoading(true); setError('')
    try {
      const [healthResponse, found] = await Promise.all([
        getGraphHealth(),
        // 服务端 operations profile 收窄默认图谱主语；先取候选再过滤，
        // 避免前端过滤把合法结果提前截断。
        searchGraphEntities({ q: query.trim(), limit: 40, profile: 'operations' }),
      ])
      setHealth(healthResponse.data)
      const items = (found.data.items ?? []).filter((item) => item.cluster_id === activeClusterId && isDefaultGraphSearchType(item.entity_type))
      setResults(items)
      const center = selected ?? items[0]
      if (!center) { setSubgraph(undefined); return }
      // 80/200 is the canvas render budget. Fetch the server's bounded graph
      // page so the equivalent relation list can report the real totals.
      setSubgraph((await getGraphNeighbors(center.entity_uid, { depth: 2, max_vertices: 300, max_edges: 1000 })).data)
    } catch (requestError: any) {
      setSubgraph(undefined); setError(graphRequestErrorMessage(requestError))
    } finally { setLoading(false) }
  }

  useEffect(() => {
    if (!activeClusterId) { setResults([]); setSubgraph(undefined) }
  }, [activeClusterId])

  return (
    <section aria-label="资源关系探索" className="graph-explorer-page">
      <PageHeader title="资源关系图谱" desc="在当前集群中查看资源之间的明确关系、方向和故障传播路径" />
      <div className="section-heading"><div><Typography.Title level={4} style={{ margin: 0 }}>关系探索</Typography.Title><Typography.Text type="secondary">在当前集群中选择任一平台资源，查看一跳关系与故障传播路径</Typography.Text></div><Tag>{activeClusterId || '未选择集群'}</Tag></div>
      <Card size="small">
        <Space wrap>
          <Input.Search aria-label="资源关系搜索" value={query} onChange={(event) => setQuery(event.target.value)} onSearch={() => void load()} placeholder="搜索资源名称或 UID（至少 2 个字符）" enterButton="探索" style={{ width: 330 }} />
          <Select aria-label="关系域过滤" value={domain} onChange={setDomain} options={DOMAIN_OPTIONS} style={{ width: 150 }} />
          <Button onClick={() => { setQuery(''); setResults([]); setSubgraph(undefined); setError('') }}>清除</Button>
        </Space>
        {results.filter((item) => !domain || resourceDomainOf(item.entity_type) === domain).map((item) => <Button key={item.entity_uid} type={subgraph?.center_entity_uid === item.entity_uid ? 'primary' : 'link'} onClick={() => void load(item)} style={{ margin: '10px 8px 0 0' }}><Tag>{resourceTypeLabel(item.entity_type)}</Tag>{item.name}</Button>)}
      </Card>
      {loading && <DataState kind="loading" compact title="正在读取关系" description="正在读取当前集群的图谱关系" />}
      {!loading && error && <DataState kind="error" title="关系图读取失败" description={error} onRetry={() => void load()} />}
      {!loading && !error && subgraph && <><div style={{ margin: '16px 0' }}><GraphSummary subgraph={subgraph} health={health} /></div><GraphExplorer subgraph={subgraph} /></>}
      {!loading && !error && !subgraph && <DataState kind="empty" title="开始关系探索" description={activeClusterId ? '输入资源名称或 UID，选择一个资源查看关系' : '请先选择已授权集群'} />}
    </section>
  )
}
