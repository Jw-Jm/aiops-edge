import React, { useEffect, useState } from 'react'
import { Button, Card, Input, Select, Space, Tag, Typography } from 'antd'
import { getGraphHealth, getGraphNeighbors, searchGraphEntities } from '../../api/knowledgeGraph'
import type { GraphEntity, GraphHealth, GraphSubgraph } from '../../api/graphContracts'
import GraphExplorer from '../../components/graph/GraphExplorer'
import GraphSummary from '../../components/graph/GraphSummary'
import DataState from '../../components/display/DataState'
import { useScopeStore } from '../../store/scopeStore'
import { isSelectableResourceType, resourceDomainOf, resourceTypeLabel } from '../../features/resources/resourceDomain'

const DOMAIN_OPTIONS = [{ value: '', label: '全部资源域' }, { value: 'compute', label: '计算' }, { value: 'network', label: '网络' }, { value: 'storage', label: '存储' }, { value: 'kubernetes', label: 'Kubernetes' }, { value: 'application', label: '应用服务' }]

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
        searchGraphEntities({ q: query.trim(), limit: 40 }),
      ])
      setHealth(healthResponse.data)
      const items = (found.data.items ?? []).filter((item) => item.cluster_id === activeClusterId && isSelectableResourceType(item.entity_type))
      setResults(items)
      const center = selected ?? items[0]
      if (!center) { setSubgraph(undefined); return }
      setSubgraph((await getGraphNeighbors(center.entity_uid, { depth: 2, max_vertices: 300, max_edges: 800 })).data)
    } catch (requestError: any) {
      setSubgraph(undefined); setError(requestError?.response?.data?.error || requestError?.message || '关系图读取失败')
    } finally { setLoading(false) }
  }

  useEffect(() => {
    if (!activeClusterId) { setResults([]); setSubgraph(undefined) }
  }, [activeClusterId])

  return (
    <section aria-label="资源关系探索" className="graph-explorer-page">
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
