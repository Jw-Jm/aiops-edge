import { api } from './client'
import type { GraphEntity, GraphHealth, GraphSubgraph } from './graphContracts'

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
export const getRunGraphContext = (runId: string) =>
  api.get<Record<string, unknown>>(`/ai/runs/${encodeURIComponent(runId)}/graph-context`)

export const getGraphOpsSyncStates = () => api.get('/ai/kg/ops/sync-states')
export const getGraphOpsOutbox = () => api.get('/ai/kg/ops/outbox')
export const getGraphOpsAliases = () => api.get('/ai/kg/ops/aliases')
export const getGraphOpsShadowDiff = () => api.get('/ai/kg/ops/shadow-diff')
