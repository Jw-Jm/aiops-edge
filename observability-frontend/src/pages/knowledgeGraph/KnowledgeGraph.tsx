import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Input, Select, Table, Tag } from 'antd'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  getGraphHealth,
  getGraphImpact,
  getGraphNeighbors,
  getGraphOpsSyncStates,
  searchGraphEntities,
} from '../../api/knowledgeGraph'
import type { GraphEdge, GraphEntity, GraphSubgraph } from '../../api/graphContracts'
import { api } from '../../api/client'
import { getTopologyNode, type TopologyNodeFact } from '../../api/telemetry'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import DataState from '../../components/display/DataState'
import { Empty, PageHeader, PaneCard, StatusBadge } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'

export const MAX_NODES = 30
export const MAX_EDGES = 60

function formatTime(ms?: number | null): string {
  if (!ms || !Number.isFinite(ms)) return '未提供'
  return new Date(ms).toLocaleString('zh-CN', { hour12: false, timeZoneName: 'short' })
}

interface ImpactEntry {
  entity_uid: string
  name?: string
  depth?: number
  relation?: string
}

const KnowledgeGraph: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')

  const [subgraph, setSubgraph] = useState<GraphSubgraph | null>(null)
  const [graphState, setGraphState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'>('loading')
  const [graphError, setGraphError] = useState<unknown>()
  const [hops, setHops] = useState<1 | 2>(1)
  const [impact, setImpact] = useState<ImpactEntry[]>([])
  const [impactState, setImpactState] = useState<'idle' | 'loading' | 'ready' | 'error' | 'empty'>('idle')
  const [Candidates, setCandidates] = useState<GraphEntity[]>([])
  const [recommendReason, setRecommendReason] = useState<string>('')
  const [searchQuery, setSearchQuery] = useState('')
  const [searchResults, setSearchResults] = useState<GraphEntity[]>([])
  const [searching, setSearching] = useState(false)

  const rootEntityId = searchParams.get('selectedRootEntityId') ?? ''
  const selectedRelationId = searchParams.get('selectedRelationId') ?? ''

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next, { replace: true })
  }

  /** 直接进入时按活动告警 + 图谱质量生成推荐关注对象（服务端真实数据） */
  const loadRecommendation = useCallback(async () => {
    try {
      const [alerts, health] = await Promise.all([
        api.get('/alerts/events', { params: { limit: 20 } }).catch(() => null),
        getGraphHealth().catch(() => null),
      ])
      const raw: any = alerts?.data
      const list: any[] = Array.isArray(raw) ? raw : (raw?.events ?? raw?.data ?? [])
      const active = list.filter((a) => a?.status !== 'resolved')
      const firstObject = active
        .map((a) => String(a?.object ?? a?.service_name ?? '').split(',')[0]?.trim())
        .find((name) => Boolean(name))
      if (firstObject) {
        const res = await searchGraphEntities({ q: firstObject, limit: 10 })
        const items = res.data?.items ?? []
        if (items.length > 0) {
          setCandidates(items)
          setRecommendReason(
            `来自活动告警（对象 ${firstObject}），结合图谱质量 ${health?.data?.ready ? '已就绪' : '未就绪'} 推荐关注`,
          )
          return
        }
      }
      setCandidates([])
      setRecommendReason(
        active.length === 0
          ? '当前窗口没有活动告警，无法自动推荐关注对象；请搜索并选择中心对象'
          : '活动告警对象在图谱中没有匹配实体，请搜索并选择中心对象',
      )
    } catch {
      setCandidates([])
      setRecommendReason('推荐来源不可用，请手动搜索选择中心对象')
    }
  }, [])

  const loadSubgraph = useCallback(() => {
    if (!rootEntityId) {
      setSubgraph(null)
      setGraphState('empty')
      return
    }
    const controller = new AbortController()
    setGraphState('loading')
    getGraphNeighbors(rootEntityId, { depth: hops, max_vertices: MAX_NODES, max_edges: MAX_EDGES })
      .then((res) => {
        const data = res.data
        setSubgraph(data)
        if ((data.vertices ?? []).length === 0) setGraphState('empty')
        else if (data.meta?.partial) setGraphState('partial')
        else if (data.meta?.stale) setGraphState('stale')
        else setGraphState('ready')
      })
      .catch((error) => {
        setGraphError(error)
        setGraphState(error?.response?.status === 403 ? 'forbidden' : 'error')
      })
    return () => controller.abort()
  }, [rootEntityId, hops])

  useEffect(() => {
    if (!rootEntityId) {
      void loadRecommendation()
      setSubgraph(null)
      setGraphState('empty')
    }
    return loadSubgraph()
  }, [rootEntityId, loadSubgraph, loadRecommendation])

  useEffect(() => {
    if (!rootEntityId) {
      setImpact([])
      setImpactState('idle')
      return
    }
    setImpactState('loading')
    getGraphImpact(rootEntityId, { max_depth: 3 })
      .then((res) => {
        const data: any = res.data
        const list: ImpactEntry[] = data?.affected ?? data?.entities ?? data?.items ?? []
        setImpact(Array.isArray(list) ? list : [])
        setImpactState(Array.isArray(list) && list.length > 0 ? 'ready' : 'empty')
      })
      .catch(() => {
        setImpact([])
        setImpactState('error')
      })
  }, [rootEntityId])

  const [topologyFact, setTopologyFact] = useState<TopologyNodeFact | null>(null)
  const [topologyState, setTopologyState] = useState<'idle' | 'loading' | 'ready' | 'empty' | 'error' | 'forbidden'>('idle')
  // 平台原生拓扑节点事实：与图谱 generation 无关的实时 RED 观测，用于交叉核对
  useEffect(() => {
    const clusterId = useScopeStore.getState().authScope?.activeClusterId ?? ''
    if (!clusterId || !rootEntityId) {
      setTopologyFact(null)
      setTopologyState('idle')
      return
    }
    const controller = new AbortController()
    setTopologyState('loading')
    getTopologyNode({ clusterId, name: rootEntityId }, controller.signal)
      .then((fact) => {
        setTopologyFact(fact)
        setTopologyState(fact.calls === 0 && fact.latencyMs === 0 ? 'empty' : 'ready')
      })
      .catch((error) => setTopologyState(error?.response?.status === 403 ? 'forbidden' : 'error'))
    return () => controller.abort()
  }, [rootEntityId])
  const [syncStates, setSyncStates] = useState<any[]>([])
  useEffect(() => {
    getGraphOpsSyncStates()
      .then((res) => setSyncStates((res.data as any)?.sources ?? []))
      .catch(() => setSyncStates([]))
  }, [])

  const vertices = subgraph?.vertices ?? []
  const edges = subgraph?.edges ?? []
  const totalNodes = subgraph?.total_nodes ?? vertices.length
  const totalEdges = subgraph?.total_edges ?? edges.length
  const renderedNodes = vertices.length
  const renderedEdges = edges.length

  /** 超出上限时按类型聚合展示，并明确返回总量与渲染量（§5.4） */
  const aggregatedByType = useMemo(() => {
    const map = new Map<string, number>()
    for (const v of vertices) map.set(v.entity_type, (map.get(v.entity_type) ?? 0) + 1)
    return Array.from(map.entries()).sort((a, b) => b[1] - a[1])
  }, [vertices])

  const selectedEdge = edges.find((e) => e.edge_uid === selectedRelationId) ?? null
  const center = vertices.find((v) => v.entity_uid === rootEntityId) ?? null
  const generation = subgraph?.meta ? vertices[0]?.generation ?? null : null

  const handleSearch = async () => {
    if (!searchQuery.trim()) return
    setSearching(true)
    try {
      const res = await searchGraphEntities({ q: searchQuery.trim(), limit: 20 })
      setSearchResults(res.data?.items ?? [])
    } catch {
      setSearchResults([])
    } finally {
      setSearching(false)
    }
  }

  return (
    <div className="knowledge-graph-page" data-testid="knowledge-graph-page">
      <PageHeader
        title="知识图谱"
        desc="中心对象、有限子图、依赖路径、影响分析、关系事实与 generation；所有面板绑定同一 selectedRootEntityId"
        actions={<Button onClick={() => { void loadRecommendation(); loadSubgraph() }}>刷新</Button>}
      />

      <div className="path-header card" data-testid="graph-selection-header">
        <div className="path-header__row">
          <div>
            <span className="muted-sm">中心对象</span>
            <h2>{center?.name ?? (rootEntityId || '未选择')}</h2>
          </div>
          <div>
            <span className="muted-sm">selectedRootEntityId</span>
            <code>{rootEntityId || '—'}</code>
          </div>
          <div>
            <span className="muted-sm">selectedRelationId</span>
            <code>{selectedRelationId || '—'}</code>
          </div>
          <div>
            <span className="muted-sm">generation</span>
            <code>{generation ?? '未提供'}</code>
          </div>
          <div>
            <span className="muted-sm">asOf</span>
            <span>{subgraph?.meta?.generated_at ? formatTime(Date.parse(subgraph.meta.generated_at)) : '未提供'}</span>
          </div>
          <div>
            <span className="muted-sm">作用域</span>
            <code>{activeClusterId || '未选择集群'}</code>
          </div>
        </div>
        <p className="path-header__reason">
          <strong>选择来源：</strong>
          {rootEntityId
            ? searchParams.get('sourcePage')
              ? `继承来源页面 ${searchParams.get('sourcePage')} 的中心对象`
              : '用户搜索选择'
            : recommendReason || '尚未选择中心对象'}
        </p>
      </div>

      {(graphState === 'partial' || graphState === 'stale') && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={graphState === 'partial' ? '图谱数据部分缺失' : '图谱数据已过期'}
          description="影响分析与 AI 结论的可信度受限：部分关系未被本次 generation 覆盖，结论不得据此判定为已确认。"
        />
      )}

      <div className="observability-grid">
        <PaneCard
          title="选择中心对象"
          action={<span className="muted-sm">{searchResults.length > 0 ? `${searchResults.length} 条结果` : ''}</span>}
        >
          <Input.Search
            allowClear
            placeholder="搜索实体名称（服务 / 节点 / 卷 / 虚机…）"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onSearch={handleSearch}
            loading={searching}
            aria-label="搜索图谱实体"
          />
          {searchResults.length > 0 && (
            <ul className="graph-search-results">
              {searchResults.map((item) => (
                <li key={item.entity_uid}>
                  <button
                    type="button"
                    onClick={() => {
                      const next = new URLSearchParams(searchParams)
                      next.set('selectedRootEntityId', item.entity_uid)
                      next.delete('selectedRelationId')
                      setSearchParams(next, { replace: true })
                      setSearchResults([])
                    }}
                  >
                    <strong>{item.name}</strong>
                    <small>
                      {item.entity_type} · {item.cluster_id} · generation {item.generation}
                    </small>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {Candidates.length > 0 && !rootEntityId && (
            <div className="graph-candidates">
              <strong>推荐关注</strong>
              <ul>
                {Candidates.slice(0, 5).map((item) => (
                  <li key={item.entity_uid}>
                    <button type="button" onClick={() => setParam('selectedRootEntityId', item.entity_uid)}>
                      {item.name}（{item.entity_type}）
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {!rootEntityId && Candidates.length === 0 && <p className="muted-sm">{recommendReason}</p>}
        </PaneCard>

        <PaneCard
          title={hops === 1 ? '一跳结构关系' : '两跳结构关系'}
          action={
            <span className="muted-sm">
              返回 {totalNodes} 节点 / {totalEdges} 边 · 渲染 {renderedNodes} / {renderedEdges}
              {subgraph?.aggregated ? ' · 已按类型聚合' : ''}
            </span>
          }
        >
          <div style={{ marginBottom: 8 }}>
            <Select
              value={hops}
              onChange={(v) => setHops(v as 1 | 2)}
              options={[
                { value: 1, label: '中心对象一跳' },
                { value: 2, label: '扩展到两跳' },
              ]}
              aria-label="选择渲染跳数"
            />
            <span className="muted-sm" style={{ marginLeft: 8 }}>
              上限 {MAX_NODES} 节点 / {MAX_EDGES} 边
            </span>
          </div>
          <BoundedDataRegion state={graphState === 'partial' || graphState === 'stale' ? 'ready' : graphState} error={graphError} onRetry={loadSubgraph} meta={subgraph?.meta}>
            {vertices.length === 0 ? (
              <DataState kind="empty" title="没有可渲染的节点" description="请选择中心对象或扩大跳数" />
            ) : (
              <>
                <Table
                  size="small"
                  rowKey="entity_uid"
                  pagination={{ pageSize: 8 }}
                  dataSource={vertices}
                  columns={[
                    { title: '实体', dataIndex: 'name', key: 'name' },
                    { title: '类型', dataIndex: 'entity_type', key: 'entity_type', width: 130 },
                    { title: '状态', dataIndex: 'status', key: 'status', width: 100 },
                    { title: '来源', dataIndex: 'source', key: 'source', width: 120 },
                    { title: 'generation', dataIndex: 'generation', key: 'generation', width: 100 },
                    { title: '最后出现', dataIndex: 'last_seen_ms', key: 'last_seen_ms', render: formatTime, width: 190 },
                  ]}
                />
                {subgraph?.aggregated && (
                  <div className="graph-aggregated">
                    <strong>超出上限的节点按类型聚合</strong>
                    <div>{aggregatedByType.map(([type, count]) => <Tag key={type}>{type} × {count}</Tag>)}</div>
                  </div>
                )}
              </>
            )}
          </BoundedDataRegion>
        </PaneCard>

        <PaneCard title="关系事实" action={<span className="muted-sm">{renderedEdges} 条</span>}>
          {edges.length === 0 ? (
            <DataState kind="empty" title="当前子图没有关系" />
          ) : (
            <Table
              size="small"
              rowKey="edge_uid"
              pagination={{ pageSize: 8 }}
              dataSource={edges}
              onRow={(record) => ({
                onClick: () => setParam('selectedRelationId', record.edge_uid),
                style: { cursor: 'pointer', background: record.edge_uid === selectedRelationId ? 'var(--bg-hover, #f0f5ff)' : undefined },
              })}
              columns={[
                { title: '关系', dataIndex: 'relation_type', key: 'relation_type', width: 150 },
                { title: '方向', key: 'direction', width: 90, render: (_: unknown, r: GraphEdge) => (r.source_uid === rootEntityId ? '出' : '入') },
                { title: '传播故障', dataIndex: 'propagates_failure', key: 'propagates_failure', width: 100, render: (v: boolean) => (v ? '是' : '否') },
                { title: '来源', dataIndex: 'source', key: 'source', width: 120 },
                { title: '置信度', dataIndex: 'confidence', key: 'confidence', width: 90, render: (v: number) => (typeof v === 'number' ? v.toFixed(2) : '未提供') },
                { title: '有效时间', key: 'valid', render: (_: unknown, r: GraphEdge) => `${formatTime(r.valid_from_ms)} → ${r.valid_to_ms ? formatTime(r.valid_to_ms) : '至今'}` },
              ]}
            />
          )}
          {selectedEdge && (
            <div className="graph-edge-detail" data-testid="selected-relation-fact">
              <strong>选中关系事实</strong>
              <dl>
                <dt>edge_uid</dt><dd><code>{selectedEdge.edge_uid}</code></dd>
                <dt>关系</dt><dd>{selectedEdge.relation_type}</dd>
                <dt>置信度</dt><dd>{selectedEdge.confidence}</dd>
                <dt>generation</dt><dd>{selectedEdge.generation}</dd>
                <dt>故障传播</dt><dd>{selectedEdge.propagates_failure ? '会传播' : '不传播'}</dd>
              </dl>
            </div>
          )}
        </PaneCard>

        <PaneCard title="影响分析" action={<span className="muted-sm">{impact.length} 个受影响对象</span>}>
          {impactState === 'idle' && <DataState kind="empty" title="未选择中心对象" />}
          {impactState === 'loading' && <DataState kind="loading" />}
          {impactState === 'error' && <DataState kind="error" description="影响分析读取失败" />}
          {impactState === 'empty' && <DataState kind="empty" title="没有下游受影响对象" description="当前 generation 未发现从中心对象传播的故障路径" />}
          {impactState === 'ready' && (
            <Table
              size="small"
              rowKey="entity_uid"
              pagination={{ pageSize: 8 }}
              dataSource={impact}
              columns={[
                { title: '受影响对象', dataIndex: 'name', key: 'name', render: (v: string, r: ImpactEntry) => v || r.entity_uid },
                { title: '深度', dataIndex: 'depth', key: 'depth', width: 80 },
                { title: '通过关系', dataIndex: 'relation', key: 'relation', width: 150 },
              ]}
            />
          )}
        </PaneCard>

        <PaneCard title="平台原生拓扑节点事实" action={<span className="muted-sm">用于与图谱关系交叉核对</span>}>
          {topologyState === 'idle' && <DataState kind="empty" title="未选择中心对象或作用域" />}
          {topologyState === 'loading' && <DataState kind="loading" />}
          {topologyState === 'forbidden' && <DataState kind="forbidden" />}
          {topologyState === 'error' && <DataState kind="error" description="原生拓扑查询失败；不显示 0 代替未知" />}
          {topologyState === 'empty' && <DataState kind="empty" title="该对象在窗口内没有调用样本" description="这是真实空结果，不是来源不可用" />}
          {topologyState === 'ready' && topologyFact && (
            <Table
              size="small"
              rowKey="metric"
              pagination={false}
              dataSource={[
                { metric: '调用次数', value: topologyFact.calls ?? '未提供' },
                { metric: '错误数', value: topologyFact.errors ?? '未提供' },
                { metric: '错误率', value: topologyFact.errorRate === null ? '未提供' : `${(topologyFact.errorRate * 100).toFixed(2)}%` },
                { metric: '平均延迟 (ms)', value: topologyFact.latencyMs ?? '未提供' },
                { metric: '吞吐', value: topologyFact.throughput ?? '未提供' },
                { metric: '健康分 / Apdex', value: `${topologyFact.healthScore ?? '未提供'} / ${topologyFact.apdex ?? '未提供'}` },
              ]}
              columns={[
                { title: '指标', dataIndex: 'metric', key: 'metric', width: 200 },
                { title: '值', dataIndex: 'value', key: 'value' },
              ]}
            />
          )}
        </PaneCard>

        <PaneCard title="图谱自动更新摘要" action={<span className="muted-sm">{syncStates.length} 个来源</span>}>
          {syncStates.length === 0 ? (
            <DataState kind="empty" title="没有可用的来源同步状态" description="调度、来源与对账控制位于设置页的图谱分区" />
          ) : (
            <>
              <Table
                size="small"
                rowKey={(r: any) => r.source ?? r.source_id ?? String(Math.random())}
                pagination={{ pageSize: 8 }}
                dataSource={syncStates}
                columns={[
                  { title: '来源', dataIndex: 'source', key: 'source' },
                  { title: 'cadence(s)', dataIndex: 'cadence_seconds', key: 'cadence_seconds' },
                  { title: '阶段', dataIndex: 'stage', key: 'stage' },
                  { title: '质量', dataIndex: 'quality', key: 'quality', render: (v: string) => <StatusBadge text={v || '未知'} tone={v === 'healthy' ? 'ok' : v === 'failed' ? 'crit' : 'warn'} /> },
                  { title: '最后成功', dataIndex: 'last_success_at', key: 'last_success_at', render: (v: string) => formatTime(v ? Date.parse(v) : undefined) },
                ]}
              />
              <Button type="link" onClick={() => navigate('/settings?section=graph')}>
                前往设置查看调度、对账与回滚
              </Button>
            </>
          )}
        </PaneCard>
      </div>

      <div className="path-cta">
        <Button
          type="primary"
          disabled={!rootEntityId}
          onClick={() =>
            navigate(
              `/ai-operations?selectedRootEntityId=${encodeURIComponent(rootEntityId)}${selectedRelationId ? `&selectedRelationId=${encodeURIComponent(selectedRelationId)}` : ''}&sourcePage=/knowledge-graph`,
            )
          }
        >
          交给 AI 分析该对象
        </Button>
      </div>
    </div>
  )
}

export default KnowledgeGraph
