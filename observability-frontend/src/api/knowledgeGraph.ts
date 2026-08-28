import { api } from './client'
import type { GraphEntity, GraphHealth, GraphSubgraph } from './graphContracts'

export interface PanoramaService {
  entity_uid?: string
  service_name: string
  namespace?: string
  application_uid?: string
  application_name?: string
  health: 'healthy' | 'degraded' | 'critical' | 'unknown' | string
  calls: number
  errors: number
  error_rate: number
  avg_latency_ms: number
}

export interface PanoramaGroup {
  group_uid: string
  name: string
  group_by: 'application' | 'namespace' | string
  service_count: number
  healthy: number
  degraded: number
  critical: number
  calls: number
  errors: number
  error_rate: number
  services: PanoramaService[]
}

export interface PanoramaGroupEdge {
  source_group_uid: string
  target_group_uid: string
  source_name: string
  target_name: string
  routes: number
  calls: number
  errors: number
  error_rate: number
  latency_ms: number
}

export interface ServiceMapResponse {
  group_by: 'application' | 'namespace' | string
  groups: PanoramaGroup[]
  services: PanoramaService[]
  aggregated_edges: PanoramaGroupEdge[]
  topology_revision: string
  warnings?: string[]
}

export interface ServiceOverviewResponse {
  total: number
  healthy: number
  degraded: number
  critical: number
  calls: number
  errors: number
  error_rate: number
  avg_latency_ms: number
  p95_latency_ms: number
  cross_namespace_edges: number
  cycle_count: number
  top_abnormal_services: PanoramaService[]
  top_error_edges: PanoramaEdge[]
  top_latency_edges: PanoramaEdge[]
  topology_revision: string
  warnings?: string[]
}

export interface PanoramaEdge {
  source_service: string
  target_service: string
  calls: number
  errors: number
  error_rate: number
  latency_ms: number
  cross_namespace: boolean
}

export interface ServiceMatrixCell extends PanoramaEdge {
  source_uid: string
  target_uid: string
}

export interface ServiceMatrixResponse {
  services: PanoramaService[]
  row_order: string[]
  column_order: string[]
  cells: ServiceMatrixCell[]
  topology_revision: string
  warnings?: string[]
}

export interface ServiceDependenciesResponse {
  center: GraphEntity
  upstream: GraphEntity[]
  downstream: GraphEntity[]
  middleware: GraphEntity[]
  edges: GraphSubgraph['edges']
  cycles: string[][]
  topology_revision: string
  meta: GraphSubgraph['meta']
}

export const getGraphHealth = () => api.get<GraphHealth>('/ai/kg/health')
export const searchGraphEntities = (params: { q: string; entity_type?: string; limit?: number }) =>
  api.get<{ items: GraphEntity[]; count: number }>('/ai/kg/entities/search', { params })
export const getGraphEntity = (uid: string) =>
  api.get<GraphEntity>(`/ai/kg/entities/${encodeURIComponent(uid)}`)
export const getGraphNeighbors = (uid: string, params?: { direction?: string; depth?: number; relation_types?: string; max_vertices?: number; max_edges?: number }) =>
  api.get<GraphSubgraph>(`/ai/kg/entities/${encodeURIComponent(uid)}/neighbors`, { params })
export const getGraphCandidate = (uid: string, params?: { depth?: number; max_vertices?: number; max_edges?: number }) =>
  api.get<GraphSubgraph>(`/ai/kg/entities/${encodeURIComponent(uid)}/candidate`, { params })
export const getGraphImpact = (uid: string, params?: { max_depth?: number }) =>
  api.get<GraphSubgraph>(`/ai/kg/entities/${encodeURIComponent(uid)}/impact`, { params })
export const getGraphPath = (source_entity_uid: string, target_entity_uid: string, max_depth = 6) =>
  api.post<GraphSubgraph>('/ai/kg/path', { source_entity_uid, target_entity_uid, max_depth })

export const getServiceOverview = (params?: { minutes?: number; namespace?: string; application_uid?: string }) =>
  api.get<ServiceOverviewResponse>('/services/overview', { params })
export const getServiceMap = (params?: { minutes?: number; group_by?: 'application' | 'namespace'; namespace?: string; application_uid?: string }) =>
  api.get<ServiceMapResponse>('/services/map', { params })
export const getServiceDependencies = (entityUID: string, params?: { minutes?: number; upstream_depth?: number; downstream_depth?: number; include_middleware?: boolean }) =>
  api.get<ServiceDependenciesResponse>(`/services/${encodeURIComponent(entityUID)}/dependencies`, { params })
export const getServiceDependencyMatrix = (params?: { minutes?: number; namespace?: string; application_uid?: string }) =>
  api.get<ServiceMatrixResponse>('/services/dependency-matrix', { params })
export const getRunGraphContext = (runId: string) =>
  api.get<Record<string, unknown>>(`/ai/runs/${encodeURIComponent(runId)}/graph-context`)

export const getGraphOpsSyncStates = () => api.get('/ai/kg/ops/sync-states')
export const getGraphOpsOutbox = () => api.get('/ai/kg/ops/outbox')
export const getGraphOpsAliases = () => api.get('/ai/kg/ops/aliases')
export const getGraphOpsShadowDiff = () => api.get('/ai/kg/ops/shadow-diff')
