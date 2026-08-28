import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Button, Card, Checkbox, Empty as AntEmpty, Input, Select, Space, Table } from 'antd'
import { useUIStore } from '../../store/uiStore'
import { getGraphHealth, getGraphImpact, getGraphNeighbors, searchGraphEntities, getServiceDependencies, getServiceDependencyMatrix, getServiceMap, getServiceOverview } from '../../api/knowledgeGraph'
import type { GraphHealth, GraphSubgraph } from '../../api/graphContracts'
import type { PanoramaService, ServiceDependenciesResponse, ServiceMapResponse, ServiceMatrixResponse, ServiceOverviewResponse } from '../../api/knowledgeGraph'
import { getServices } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import { normalizeErrorRate } from '../../lib/errorRate'
import GraphSummary from '../../components/graph/GraphSummary'
import GraphExplorer from '../../components/graph/GraphExplorer'
import ImpactTree from '../../components/graph/ImpactTree'
import ServiceMapView from '../../components/graph/ServiceMapView'
import ServiceDependencyView from '../../components/graph/ServiceDependencyView'
import CallMatrix from '../../components/graph/CallMatrix'
import ServiceSummary from './service/ServiceSummary'

type ServiceRow = PanoramaService & { service: string; avg_latency_ms: number }
const EMPTY_GRAPH: GraphSubgraph = { center_entity_uid: '', vertices: [], edges: [], meta: { contract_version: 'graph-dto-v1', schema_version: 0, partial: false, stale: true, generated_at: '', warning_codes: [] } }

function normalizeLegacyServices(payload: any): ServiceRow[] {
  const rows = Array.isArray(payload) ? payload : payload?.data || payload?.services || []
  return rows.map((row: any) => {
    const service = String(row.service_name ?? row.service ?? '')
    const calls = Number(row.traces ?? row.calls ?? row.spans ?? 0)
    const errors = Number(row.errors ?? 0)
    return { entity_uid: row.entity_uid, service_name: service, service, namespace: row.namespace, application_name: row.application_name, health: row.health ?? 'unknown', calls, errors, error_rate: normalizeErrorRate(row.error_rate ?? row.errorRate ?? (calls ? errors / calls : 0)), avg_latency_ms: Number(row.avg_ms ?? row.avg_latency_ms ?? row.latency_ms ?? 0) }
  }).filter((row: ServiceRow) => row.service && !row.service.includes('(deleted)'))
}

function legacyMap(rows: ServiceRow[]): ServiceMapResponse {
  const groups = new Map<string, ServiceMapResponse['groups'][number]>()
  for (const service of rows) {
    const namespace = service.namespace || '(未分组)'
    const groupUID = `namespace:${namespace}`
    const group = groups.get(groupUID) ?? { group_uid: groupUID, name: namespace, group_by: 'namespace', service_count: 0, healthy: 0, degraded: 0, critical: 0, calls: 0, errors: 0, error_rate: 0, services: [] }
    group.services.push(service); group.service_count++; group.calls += service.calls; group.errors += service.errors
    if (service.health === 'healthy') group.healthy++; else if (service.health === 'degraded') group.degraded++; else if (service.health === 'critical') group.critical++
    group.error_rate = group.calls ? group.errors / group.calls : 0
    groups.set(groupUID, group)
  }
  return { group_by: 'namespace', groups: [...groups.values()], services: rows, aggregated_edges: [], topology_revision: 'legacy-fallback' }
}

const ServiceObservability: React.FC = () => {
  const currentClusterId = useUIStore((state) => state.currentClusterId)
  const [timeRange, setTimeRange] = useState(60)
  const [services, setServices] = useState<ServiceRow[]>([])
  const [selectedService, setSelectedService] = useState('')
  const [selectedEntityUID, setSelectedEntityUID] = useState('')
  const [serviceFilter, setServiceFilter] = useState('')
  const [namespaceFilter, setNamespaceFilter] = useState('')
  const [applicationFilter, setApplicationFilter] = useState('')
  const [healthFilter, setHealthFilter] = useState('')
  const [onlyAbnormal, setOnlyAbnormal] = useState(false)
  const [mapData, setMapData] = useState<ServiceMapResponse>()
  const mapDataRef = useRef<ServiceMapResponse>()
  const mapFilterRef = useRef('')
  mapDataRef.current = mapData
  const [overview, setOverview] = useState<ServiceOverviewResponse>()
  const [matrix, setMatrix] = useState<ServiceMatrixResponse>()
  const [dependency, setDependency] = useState<ServiceDependenciesResponse>()
  const [graph, setGraph] = useState<GraphSubgraph>(EMPTY_GRAPH)
  const [impactGraph, setImpactGraph] = useState<GraphSubgraph>(EMPTY_GRAPH)
  const [health, setHealth] = useState<GraphHealth>()
  const [loading, setLoading] = useState(true)
  const [mapLoading, setMapLoading] = useState(false)
  const [matrixLoading, setMatrixLoading] = useState(false)
  const [dependencyLoading, setDependencyLoading] = useState(false)
  const [error, setError] = useState('')
  const [structureRefresh, setStructureRefresh] = useState(0)

  const loadOverview = useCallback(async () => {
    setLoading(true); setError('')
    try {
      if (typeof getServiceOverview === 'function' && typeof getServiceMap === 'function') {
        const filters = { minutes: timeRange, ...(namespaceFilter ? { namespace: namespaceFilter } : {}), ...(applicationFilter ? { application_uid: applicationFilter } : {}) }
        const filterKey = `${namespaceFilter}\u0000${applicationFilter}`
        const [overviewResponse, healthResponse] = await Promise.all([getServiceOverview(filters), getGraphHealth()])
        const nextOverview = overviewResponse.data
        let nextMap = mapDataRef.current
        if (!nextMap || nextMap.topology_revision !== nextOverview.topology_revision || mapFilterRef.current !== filterKey) {
          nextMap = (await getServiceMap({ ...filters, group_by: nextMap?.group_by === 'namespace' ? 'namespace' : 'application' })).data
        }
        if (!nextMap) throw new Error('service map response is empty')
        mapFilterRef.current = filterKey
        setOverview(nextOverview); setMapData(nextMap); setHealth(healthResponse.data)
        const rows = nextMap.services.map((service) => ({ ...service, service: service.service_name }))
        setServices(rows)
        setSelectedService((current) => rows.some((row) => row.service === current) ? current : rows[0]?.service || '')
        setSelectedEntityUID((current) => rows.find((row) => row.service === current)?.entity_uid || rows[0]?.entity_uid || '')
      } else {
        const [serviceResponse, healthResponse] = await Promise.all([getServices({ minutes: timeRange }), getGraphHealth()])
        const rows = normalizeLegacyServices(serviceResponse.data); setServices(rows); setMapData(legacyMap(rows)); setHealth(healthResponse.data)
        setSelectedService((current) => rows.some((row) => row.service === current) ? current : rows[0]?.service || '')
      }
    } catch {
      setError('服务摘要或服务地图暂时不可用，请检查 query-api 与事实数据源状态。')
      setServices([]); setMapData(undefined); setOverview(undefined)
    } finally { setLoading(false) }
  }, [applicationFilter, namespaceFilter, timeRange])

  const loadMatrix = useCallback(async () => {
    if (typeof getServiceDependencyMatrix !== 'function') return
    setMatrixLoading(true)
    try { setMatrix((await getServiceDependencyMatrix({ minutes: timeRange, ...(namespaceFilter ? { namespace: namespaceFilter } : {}), ...(applicationFilter ? { application_uid: applicationFilter } : {}) })).data) } catch { setError('调用矩阵暂时不可用，请检查 query-api。') } finally { setMatrixLoading(false) }
  }, [applicationFilter, namespaceFilter, timeRange])

  useEffect(() => { void loadOverview(); void loadMatrix() }, [currentClusterId, loadMatrix, loadOverview])
  useEffect(() => { const timer = window.setInterval(() => { void loadOverview() }, 30_000); return () => window.clearInterval(timer) }, [loadOverview])

  useEffect(() => {
    const row = services.find((service) => service.service === selectedService)
    if (row?.entity_uid) setSelectedEntityUID(row.entity_uid)
  }, [selectedService, services])

  // Compatibility fallback for old deployments that have not exposed the
  // panorama contracts yet: resolve the selected service through the typed
  // graph search before asking for its dependency view.
  useEffect(() => {
    if (selectedEntityUID || !selectedService || typeof searchGraphEntities !== 'function') return
    let cancelled = false
    void searchGraphEntities({ q: selectedService, entity_type: 'service', limit: 5 }).then((response) => {
      const entity = response.data?.items?.[0]
      if (!cancelled && entity?.entity_uid) setSelectedEntityUID(entity.entity_uid)
    }).catch(() => undefined)
    return () => { cancelled = true }
  }, [selectedEntityUID, selectedService])

  useEffect(() => {
    let cancelled = false
    async function loadDependency() {
      if (!selectedEntityUID) { setDependency(undefined); setGraph(EMPTY_GRAPH); setImpactGraph(EMPTY_GRAPH); return }
      setDependencyLoading(true)
      try {
        if (typeof getServiceDependencies === 'function') {
          const response = await getServiceDependencies(selectedEntityUID, { minutes: timeRange, upstream_depth: 1, downstream_depth: 1 })
          if (!cancelled) { setDependency(response.data); setGraph({ center_entity_uid: response.data.center.entity_uid, vertices: [response.data.center, ...response.data.upstream, ...response.data.downstream, ...response.data.middleware], edges: response.data.edges, meta: response.data.meta }) }
        } else {
          const response = await getGraphNeighbors(selectedEntityUID, { depth: 2, direction: 'BOTH', max_vertices: 300, max_edges: 1000 })
          if (!cancelled) setGraph(response.data)
        }
        try { const impact = await getGraphImpact(selectedEntityUID, { max_depth: 3 }); if (!cancelled) setImpactGraph(impact.data) } catch { if (!cancelled) setImpactGraph(EMPTY_GRAPH) }
      } catch { if (!cancelled) { setDependency(undefined); setGraph(EMPTY_GRAPH); setError('依赖主链暂时不可用，请检查图谱状态。') } } finally { if (!cancelled) setDependencyLoading(false) }
    }
    void loadDependency(); return () => { cancelled = true }
  }, [selectedEntityUID, structureRefresh, timeRange])

  const visibleServices = useMemo(() => services.filter((row) => row.service.toLowerCase().includes(serviceFilter.toLowerCase())).filter((row) => !healthFilter || row.health === healthFilter).filter((row) => !onlyAbnormal || ['degraded', 'critical'].includes(row.health)), [healthFilter, onlyAbnormal, serviceFilter, services])
  const fallbackGraph = graph.vertices.length > 0 ? graph : EMPTY_GRAPH

  return <div className="page">
    <Breadcrumb items={[{ t: '可观测性' }, { t: '服务全景' }]} />
    <PageHeader title="服务全景" desc="服务摘要 → 服务地图 → 依赖主链 → 调用矩阵 → 服务列表 → 专家关系探索。默认地图按 Application/Namespace 聚合，原始关系仅在专家探索中展开。" actions={<Space wrap><Select aria-label="时间范围" value={timeRange} onChange={setTimeRange} options={[{ value: 15, label: '近 15 分钟' }, { value: 60, label: '近 1 小时' }, { value: 1440, label: '近 24 小时' }]} /><Select allowClear aria-label="命名空间" placeholder="命名空间" value={namespaceFilter || undefined} onChange={(value) => setNamespaceFilter(value || '')} options={[...new Set(services.map((service) => service.namespace).filter(Boolean))].map((value) => ({ value, label: value }))} /><Select allowClear aria-label="应用" placeholder="应用" value={applicationFilter || undefined} onChange={(value) => setApplicationFilter(value || '')} options={[...new Map(services.filter((service) => service.application_uid).map((service) => [service.application_uid, service.application_name || service.application_uid])).entries()].map(([value, label]) => ({ value, label }))} /><Select allowClear aria-label="健康状态" placeholder="健康状态" value={healthFilter || undefined} onChange={(value) => setHealthFilter(value || '')} options={[{ value: 'healthy', label: '健康' }, { value: 'degraded', label: '降级' }, { value: 'critical', label: '严重' }]} /><Checkbox checked={onlyAbnormal} onChange={(event) => setOnlyAbnormal(event.target.checked)}>仅看异常</Checkbox><Button onClick={() => void loadOverview()}>刷新摘要</Button></Space>} />
    {error && <Alert type="warning" showIcon message={error} style={{ marginBottom: 16 }} />}
    <section aria-label="服务摘要"><ServiceSummary overview={overview} services={services} health={health} timeRange={timeRange} loading={loading} /></section>
    <section aria-label="服务地图" style={{ marginTop: 16 }}><Card title="服务地图" loading={mapLoading} extra={<Space><Select aria-label="地图分组" size="small" value={mapData?.group_by || 'application'} options={[{ value: 'application', label: '按 Application' }, { value: 'namespace', label: '按 Namespace' }]} onChange={async (group_by) => { const selectedGroup = group_by as 'application' | 'namespace'; setMapLoading(true); try { if (typeof getServiceMap === 'function') setMapData((await getServiceMap({ minutes: timeRange, group_by: selectedGroup, ...(namespaceFilter ? { namespace: namespaceFilter } : {}), ...(applicationFilter ? { application_uid: applicationFilter } : {}) })).data) } finally { setMapLoading(false) } }} /><Button size="small" onClick={() => void loadOverview()}>刷新摘要</Button></Space>}>{mapData ? <ServiceMapView data={mapData} onServiceSelect={setSelectedService} /> : <AntEmpty description="暂无服务地图数据" />}</Card><div style={{ marginTop: 12 }}><GraphSummary subgraph={fallbackGraph} health={health} /></div></section>
    <section style={{ marginTop: 16 }}><ServiceDependencyView data={dependency} onEntitySelect={(entity) => { setSelectedEntityUID(entity.entity_uid); setSelectedService(entity.name) }} /></section>
    <section style={{ marginTop: 16 }}><Card loading={matrixLoading} extra={<Button size="small" onClick={() => void loadMatrix()}>刷新矩阵</Button>}><CallMatrix matrix={matrix} vertices={fallbackGraph.vertices} edges={fallbackGraph.edges} /></Card></section>
    <section aria-label="服务列表" style={{ marginTop: 16 }}><Card title="服务列表" extra={<Input allowClear placeholder="筛选服务" value={serviceFilter} onChange={(event) => setServiceFilter(event.target.value)} style={{ width: 220 }} />}><Table<ServiceRow> rowKey="service" size="small" dataSource={visibleServices} pagination={{ pageSize: 20, showSizeChanger: false }} columns={[{ title: '服务', dataIndex: 'service', render: (value: string, row) => <Button type="link" onClick={() => { setSelectedService(value); if (row.entity_uid) setSelectedEntityUID(row.entity_uid) }}>{value}</Button> }, { title: '调用量', dataIndex: 'calls' }, { title: '错误数', dataIndex: 'errors' }, { title: '错误率', dataIndex: 'error_rate', render: (value: number) => `${(value * 100).toFixed(1)}%` }, { title: '平均延迟', dataIndex: 'avg_latency_ms', render: (value: number) => `${value.toFixed(0)}ms` }]} locale={{ emptyText: '暂无服务指标' }} /></Card></section>
    <section aria-label="专家关系探索" style={{ marginTop: 16 }}><Card title="专家关系探索" extra={<Button size="small" onClick={() => setStructureRefresh((value) => value + 1)}>刷新结构</Button>}><GraphExplorer subgraph={fallbackGraph} /><div style={{ marginTop: 16 }}><ImpactTree subgraph={impactGraph} /></div></Card></section>
  </div>
}

export default ServiceObservability
