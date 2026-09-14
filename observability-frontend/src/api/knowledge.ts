import { api } from './client'

export type KnowledgeScopeType = 'platform_common' | 'cluster'
export type KnowledgeStatus = 'draft' | 'pending_review' | 'published' | 'disabled'
export type KnowledgeType = 'incident' | 'document' | 'playbook'

export interface KnowledgeItem {
  knowledge_id: string
  tenant_id?: string
  scope_type: KnowledgeScopeType
  cluster_id?: string
  knowledge_type: KnowledgeType | string
  title: string
  summary?: string
  status: KnowledgeStatus | string
  source_kind?: string
  source_revision?: string
  current_version_id?: string
  draft_version_id?: string
  created_at?: string
  updated_at?: string
}

export interface KnowledgeVersion {
  version_id: string
  knowledge_id?: string
  version_number?: number
  content?: string
  source_kind?: string
  source_revision?: string
  content_sha256?: string
  created_at?: string
}

export interface KnowledgeFilters {
  knowledgeType?: string
  status?: string
  query?: string
}

export interface KnowledgeListResponse {
  items: KnowledgeItem[]
  meta?: { index_available?: boolean; error?: string; generated_at?: string }
}

export interface KnowledgeIndexStatusResponse {
  items: Array<Record<string, unknown>>
  meta?: { index_available?: boolean; error?: string }
}

export interface KnowledgeInput {
  scope_type: KnowledgeScopeType
  cluster_id?: string
  knowledge_type: string
  title: string
  summary?: string
  content: string
  source_kind: string
  source_revision?: string
  status?: 'draft'
}

const clusterKnowledgePath = (clusterId: string, suffix = '') => `/clusters/${encodeURIComponent(clusterId)}/knowledge${suffix}`

export const listKnowledge = async (clusterId: string, filters: KnowledgeFilters = {}): Promise<KnowledgeListResponse> => {
  const params: Record<string, string> = {}
  if (filters.knowledgeType) params.knowledge_type = filters.knowledgeType
  if (filters.status) params.status = filters.status
  if (filters.query) params.query = filters.query
  const response = await api.get<KnowledgeListResponse>(clusterKnowledgePath(clusterId), Object.keys(params).length ? { params } : undefined)
  return response.data
}

export const getKnowledge = async (clusterId: string, knowledgeId: string) => {
  const response = await api.get<{ data: KnowledgeItem; version: KnowledgeVersion }>(clusterKnowledgePath(clusterId, `/${encodeURIComponent(knowledgeId)}`))
  return response.data
}

export const createKnowledge = async (clusterId: string, input: KnowledgeInput) => {
  const response = await api.post<{ data: KnowledgeItem; version: KnowledgeVersion }>(clusterKnowledgePath(clusterId), input)
  return response.data
}

export const createKnowledgeRevision = async (clusterId: string, knowledgeId: string, input: Pick<KnowledgeInput, 'content' | 'source_kind' | 'source_revision'>) => {
  const response = await api.patch<{ version: KnowledgeVersion; status: KnowledgeStatus }>(clusterKnowledgePath(clusterId, `/${encodeURIComponent(knowledgeId)}`), input)
  return response.data
}

export const submitKnowledge = (clusterId: string, knowledgeId: string) => api.post(clusterKnowledgePath(clusterId, `/${encodeURIComponent(knowledgeId)}/submit`))
export const reviewKnowledge = (clusterId: string, knowledgeId: string, decision: 'approve' | 'reject', reason: string) => api.post(clusterKnowledgePath(clusterId, `/${encodeURIComponent(knowledgeId)}/review`), { decision, reason })
export const disableKnowledge = (clusterId: string, knowledgeId: string) => api.post(clusterKnowledgePath(clusterId, `/${encodeURIComponent(knowledgeId)}/disable`))

export const getKnowledgeIndexStatus = async (clusterId: string): Promise<KnowledgeIndexStatusResponse> => {
  const response = await api.get<KnowledgeIndexStatusResponse>(clusterKnowledgePath(clusterId, '/index-status'))
  return response.data
}

export const searchKnowledge = async (clusterId: string, input: { query: string; topK?: number }) => {
  const response = await api.post<{ items: Array<Record<string, unknown>>; meta?: Record<string, unknown> }>(clusterKnowledgePath(clusterId, '/search'), { query: input.query, top_k: input.topK ?? 8 })
  return response.data
}

export const reindexKnowledge = (clusterId: string) => api.post<{ queued: number }>(clusterKnowledgePath(clusterId, '/reindex'))
