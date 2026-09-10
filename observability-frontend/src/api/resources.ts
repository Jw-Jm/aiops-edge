import { api } from './client'
import type { GraphEntityType } from './graphContracts'
import type { PlatformResourceRef, ResourceDomain, ResourceHealth } from '../features/resources/types'

export interface ResourceReadMeta {
  generated_at: string
  partial: boolean
  stale: boolean
  warning_codes: string[]
}

export interface ResourceCatalogParams {
  group?: 'containers' | 'kubevirt'
  domain?: ResourceDomain
  type?: GraphEntityType
  q?: string
  health?: ResourceHealth
  limit?: number
  cursor?: string
}

interface ResourceCatalogWireItem {
  uid: string
  cluster_id: string
  type: GraphEntityType
  domain: ResourceDomain
  name: string
  namespace?: string
  location: string
  health: ResourceHealth
  source: string
  resolution?: string
  last_seen_at?: string
  capabilities?: string[]
  attributes?: Record<string, unknown>
}

interface ResourceCatalogWireResponse {
  items: ResourceCatalogWireItem[]
  total?: number
  next_cursor?: string
  meta: ResourceReadMeta
}

export interface ResourceCatalogItem extends PlatformResourceRef {
  typeLabel?: string
  location: string
  health: ResourceHealth
  source: string
  resolution?: string
  lastSeenAt?: string
  capabilities?: string[]
  attributes?: Record<string, unknown>
}

export interface ResourceCatalogResponse {
  items: ResourceCatalogItem[]
  total: number
  nextCursor?: string
  meta: ResourceReadMetaView
}

export interface ResourceDomainSummary {
  domain: ResourceDomain
  count: number
  health: Record<string, number>
  incomplete: boolean
}

export interface ResourceSummaryResponse {
  domains: ResourceDomainSummary[]
  meta: ResourceReadMetaView
}

export interface ResourceDetailResponse {
  data: ResourceCatalogItem
  meta: ResourceReadMetaView
}

export interface ResourceReadMetaView {
  generatedAt: string
  partial: boolean
  stale: boolean
  warningCodes: string[]
}

export type ResourceApiErrorKind = 'forbidden' | 'notFound' | 'unavailable' | 'invalid' | 'unknown'

export interface ResourceApiError extends Error {
  kind: ResourceApiErrorKind
  code: string
  status?: number
}

function mapMeta(meta: ResourceReadMeta): ResourceReadMetaView {
  return { generatedAt: meta.generated_at, partial: meta.partial, stale: meta.stale, warningCodes: meta.warning_codes ?? [] }
}

function mapItem(item: ResourceCatalogWireItem): ResourceCatalogItem {
  return {
    clusterId: item.cluster_id,
    uid: item.uid,
    type: item.type,
    domain: item.domain,
    name: item.name,
    ...(item.namespace ? { namespace: item.namespace } : {}),
    location: item.location,
    health: item.health,
    source: item.source,
    ...(item.resolution ? { resolution: item.resolution } : {}),
    ...(item.last_seen_at ? { lastSeenAt: item.last_seen_at } : {}),
    ...(item.capabilities ? { capabilities: item.capabilities } : {}),
    ...(item.attributes ? { attributes: item.attributes } : {}),
  }
}

export async function getResourceCatalog(params: ResourceCatalogParams = {}, signal?: AbortSignal): Promise<ResourceCatalogResponse> {
  const response = await api.get<ResourceCatalogWireResponse>('/resources/catalog', { params, signal })
  return { items: response.data.items.map(mapItem), total: response.data.total ?? response.data.items.length, nextCursor: response.data.next_cursor, meta: mapMeta(response.data.meta) }
}

export async function getResourceSummary(signal?: AbortSignal): Promise<ResourceSummaryResponse> {
  const response = await api.get<{ domains: ResourceDomainSummary[]; meta: ResourceReadMeta }>('/resources/summary', { signal })
  return { domains: response.data.domains, meta: mapMeta(response.data.meta) }
}

export async function getResourceDetail(uid: string, signal?: AbortSignal): Promise<ResourceDetailResponse> {
  // Axios serializes query parameters; passing the raw UID avoids double-encoding
  // values such as `pod:namespace/name` before they reach the Query API.
  const response = await api.get<{ data: ResourceCatalogWireItem; meta: ResourceReadMeta }>('/resources/detail', { params: { uid }, signal })
  return { data: mapItem(response.data.data), meta: mapMeta(response.data.meta) }
}

export function toResourceApiError(error: unknown): ResourceApiError {
  const response = (error as { response?: { status?: number; data?: { error?: string } } } | null)?.response
  const status = response?.status
  const code = response?.data?.error || 'RESOURCE_REQUEST_FAILED'
  const kind: ResourceApiErrorKind = status === 403 ? 'forbidden' : status === 404 ? 'notFound' : status === 503 ? 'unavailable' : status === 400 ? 'invalid' : 'unknown'
  const result = new Error(code) as ResourceApiError
  result.name = 'ResourceApiError'
  result.kind = kind
  result.code = code
  result.status = status
  return result
}
