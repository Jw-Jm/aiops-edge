import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Col, Empty as AntEmpty, Input, Row, Select, Space, Spin, Statistic, Table, Tag } from 'antd'
import { useUIStore } from '../../store/uiStore'
import { getServices } from '../../api/client'
import { getGraphHealth, getGraphImpact, getGraphNeighbors, searchGraphEntities } from '../../api/knowledgeGraph'
import type { GraphEdge, GraphEntity, GraphHealth, GraphSubgraph } from '../../api/graphContracts'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import { normalizeErrorRate } from '../../lib/errorRate'
import GraphMap from '../../components/graph/GraphMap'
import GraphSummary from '../../components/graph/GraphSummary'
import DependencyChain from '../../components/graph/DependencyChain'
import CallMatrix from '../../components/graph/CallMatrix'
import GraphExplorer from '../../components/graph/GraphExplorer'
import ImpactTree from '../../components/graph/ImpactTree'

type ServiceRow = {
  service: string
  calls: number
  errors: number
  error_rate: number
  avg_latency_ms: number
}

const EMPTY_GRAPH: GraphSubgraph = {
  center_entity_uid: '',
  vertices: [],
  edges: [],
  meta: { contract_version: 'graph-dto-v1', schema_version: 0, partial: false, stale: true, generated_at: '', warning_codes: [] },
}

const SERVICE_TYPES = new Set(['service', 'application', 'middleware', 'k8s_service'])

function normalizeServices(payload: any): ServiceRow[] {
  const rows = Array.isArray(payload) ? payload : payload?.data || payload?.services || []
  return rows.map((row: any) => ({
    service: String(row.service_name ?? row.service ?? ''),
    calls: Number(row.traces ?? row.calls ?? row.spans ?? 0),
    errors: Number(row.errors ?? 0),
    error_rate: normalizeErrorRate(row.error_rate ?? row.errorRate ?? 0),
    avg_latency_ms: Number(row.avg_ms ?? row.avg_latency_ms ?? row.latency_ms ?? 0),
  })).filter((row: ServiceRow) => row.service && !row.service.includes('(deleted)'))
}

function serviceGraph(graph: GraphSubgraph): GraphSubgraph {
  const vertices = graph.vertices.filter((vertex) => SERVICE_TYPES.has(vertex.entity_type)).slice(0, 300)
  const ids = new Set(vertices.map((vertex) => vertex.entity_uid))
  return { ...graph, vertices, edges: graph.edges.filter((edge) => ids.has(edge.source_uid) && ids.has(edge.target_uid)).slice(0, 1000) }
}

function dependencyChain(graph: GraphSubgraph, centerUid: string): { vertices: GraphEntity[]; edges: GraphEdge[] } {
  const vertexMap = new Map(graph.vertices.map((vertex) => [vertex.entity_uid, vertex]))
  const outgoing = new Map<string, GraphEdge[]>()
  for (const edge of graph.edges) {
    if (!SERVICE_TYPES.has(vertexMap.get(edge.source_uid)?.entity_type || '') || !SERVICE_TYPES.has(vertexMap.get(edge.target_uid)?.entity_type || '')) continue
    const list = outgoing.get(edge.source_uid) || []
    list.push(edge)
    outgoing.set(edge.source_uid, list)
  }
  const vertices: GraphEntity[] = []
  const edges: GraphEdge[] = []
  const seen = new Set<string>()
  let current = centerUid
  while (current && !seen.has(current) && vertices.length < 8) {
    const vertex = vertexMap.get(current)
    if (!vertex) break
    seen.add(current)
    vertices.push(vertex)
    const next = (outgoing.get(current) || []).slice().sort((a, b) => a.target_uid.localeCompare(b.target_uid))[0]
    if (!next || seen.has(next.target_uid)) break
    edges.push(next)
    current = next.target_uid
  }
  return { vertices, edges }
}

const ServiceObservability: React.FC = () => {
  const currentClusterId = useUIStore((state) => state.currentClusterId)
  const [timeRange, setTimeRange] = useState(60)
  const [services, setServices] = useState<ServiceRow[]>([])
  const [selectedService, setSelectedService] = useState('')
  const [serviceFilter, setServiceFilter] = useState('')
  const [graph, setGraph] = useState<GraphSubgraph>(EMPTY_GRAPH)
  const [impactGraph, setImpactGraph] = useState<GraphSubgraph>(EMPTY_GRAPH)
  const [health, setHealth] = useState<GraphHealth>()
  const [loading, setLoading] = useState(true)
  const [graphLoading, setGraphLoading] = useState(false)
  const [graphRefreshToken, setGraphRefreshToken] = useState(0)
  const [error, setError] = useState('')

  const loadServices = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [serviceResponse, healthResponse] = await Promise.all([
        getServices({ minutes: timeRange }),
        getGraphHealth(),
      ])
      const rows = normalizeServices(serviceResponse.data)
      setServices(rows)
      setHealth(healthResponse.data)
      setSelectedService((current) => rows.some((row) => row.service === current) ? current : rows[0]?.service || '')
    } catch {
      setServices([])
      setHealth(undefined)
      setError('服务指标或知识图谱暂时不可用，请检查查询 API 与数据源状态。')
    } finally {
      setLoading(false)
    }
  }, [timeRange])

  useEffect(() => { void loadServices() }, [currentClusterId, loadServices])

  // Metrics and graph health are volatile; refresh those summaries every 30s.
  // The relation graph has its own effect and is intentionally not re-laid out
  // by this timer, so an operator can explore a stable topology.
  useEffect(() => {
    const timer = window.setInterval(() => { void loadServices() }, 30_000)
    return () => window.clearInterval(timer)
  }, [loadServices])

  useEffect(() => {
    let cancelled = false
    async function loadGraph() {
      if (!selectedService) { setGraph(EMPTY_GRAPH); setImpactGraph(EMPTY_GRAPH); return }
      setGraphLoading(true)
      try {
        const found = await searchGraphEntities({ q: selectedService, entity_type: 'service', limit: 5 })
        const items = found.data?.items || []
        const entity = items.find((item) => item.name === selectedService) || items[0]
        if (!entity) { if (!cancelled) setGraph(EMPTY_GRAPH); return }
        const response = await getGraphNeighbors(entity.entity_uid, { depth: 2, direction: 'BOTH', max_vertices: 300, max_edges: 1000 })
        let impact = EMPTY_GRAPH
        try {
          impact = (await getGraphImpact(entity.entity_uid, { max_depth: 3 })).data
        } catch { /* impact is optional; retain the usable neighbor graph */ }
        if (!cancelled) {
          setGraph(serviceGraph(response.data))
          setImpactGraph(impact)
        }
      } catch {
        if (!cancelled) setGraph(EMPTY_GRAPH)
      } finally {
        if (!cancelled) setGraphLoading(false)
      }
    }
    void loadGraph()
    return () => { cancelled = true }
  }, [currentClusterId, graphRefreshToken, selectedService])

  const visibleServices = useMemo(() => services.filter((row) => row.service.toLowerCase().includes(serviceFilter.toLowerCase())), [serviceFilter, services])
  const center = graph.vertices.find((vertex) => vertex.entity_uid === graph.center_entity_uid) || graph.vertices[0]
  const chain = useMemo(() => dependencyChain(graph, center?.entity_uid || ''), [center?.entity_uid, graph])
  const summary = services.find((service) => service.service === selectedService) || services[0]

  return <div className="page">
    <Breadcrumb items={[{ t: '可观测性' }, { t: '服务全景' }]} />
    <PageHeader title="服务全景" desc="以指标摘要、受控服务地图和可审计关系查询定位服务依赖。" actions={<Space><Select aria-label="时间范围" value={timeRange} onChange={setTimeRange} options={[{ value: 15, label: '近 15 分钟' }, { value: 60, label: '近 1 小时' }, { value: 1440, label: '近 24 小时' }]} /><Button onClick={() => void loadServices()}>刷新</Button></Space>} />
    {error && <Alert type="warning" showIcon message={error} style={{ marginBottom: 16 }} />}
    <section aria-label="服务摘要">
      <Card title="服务摘要" loading={loading}>
        <Row gutter={16}>
          <Col xs={12} md={6}><Statistic title="服务数" value={services.length} /></Col>
          <Col xs={12} md={6}><Statistic title="当前服务" value={summary?.service || '—'} /></Col>
          <Col xs={12} md={6}><Statistic title="调用量" value={summary?.calls ?? 0} /></Col>
          <Col xs={12} md={6}><Statistic title="错误率" value={summary ? `${(summary.error_rate * 100).toFixed(1)}%` : '—'} /></Col>
        </Row>
        <div style={{ marginTop: 12 }}><Tag color={health?.ready ? 'green' : 'red'}>图谱：{health?.ready ? `${health.backend} 就绪` : '不可用'}</Tag><Tag>窗口：近 {timeRange} 分钟</Tag></div>
      </Card>
    </section>
    <section aria-label="服务地图" style={{ marginTop: 16 }}>
      <Card title="服务地图" loading={graphLoading} extra={<Button size="small" onClick={() => setGraphRefreshToken((value) => value + 1)}>刷新关系</Button>}>
        {graph.vertices.length ? <GraphMap subgraph={graph} height={420} /> : <AntEmpty description="暂无服务关系数据" />}
      </Card>
      <div style={{ marginTop: 12 }}><GraphSummary subgraph={graph} health={health} /></div>
    </section>
    <section style={{ marginTop: 16 }}><DependencyChain vertices={chain.vertices} edges={chain.edges} /></section>
    <section style={{ marginTop: 16 }}><Card><ImpactTree subgraph={impactGraph} /></Card></section>
    <section style={{ marginTop: 16 }}><Card><CallMatrix vertices={graph.vertices} edges={graph.edges} /></Card></section>
    <section aria-label="服务列表" style={{ marginTop: 16 }}>
      <Card title="服务列表" extra={<Input allowClear placeholder="筛选服务" value={serviceFilter} onChange={(event) => setServiceFilter(event.target.value)} style={{ width: 220 }} />}>
        <Table<ServiceRow> rowKey="service" size="small" dataSource={visibleServices} pagination={{ pageSize: 20, showSizeChanger: false }} columns={[
          { title: '服务', dataIndex: 'service', render: (value: string) => <Button type="link" onClick={() => setSelectedService(value)}>{value}</Button> },
          { title: '调用量', dataIndex: 'calls' },
          { title: '错误数', dataIndex: 'errors' },
          { title: '错误率', dataIndex: 'error_rate', render: (value: number) => `${(value * 100).toFixed(1)}%` },
          { title: '平均延迟', dataIndex: 'avg_latency_ms', render: (value: number) => `${value.toFixed(0)}ms` },
        ]} locale={{ emptyText: '暂无服务指标' }} />
      </Card>
    </section>
    <section aria-label="专家关系探索" style={{ marginTop: 16 }}><Card><h3>专家关系探索</h3>{graphLoading ? <Spin /> : <GraphExplorer subgraph={graph} />}</Card></section>
  </div>
}

export default ServiceObservability
