import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { getResourceCatalog, getResourceDetail, getResourceSummary, toResourceApiError } from './resources'

afterEach(() => vi.restoreAllMocks())

describe('resource API client', () => {
  it('maps the server resource projection into the typed UI reference', async () => {
    vi.spyOn(api, 'get').mockResolvedValueOnce({ data: {
      items: [{ uid: 'k8s:pod-01', cluster_id: 'cluster-a', type: 'pod', domain: 'kubernetes', name: 'api-01', namespace: 'payments', location: 'payments / api-01', health: 'critical', source: 'graph' }],
      next_cursor: 'next', meta: { generated_at: '2026-09-09T02:00:00Z', partial: false, stale: false, warning_codes: [] },
    } } as never)

    const result = await getResourceCatalog({ domain: 'kubernetes', q: 'api' })
    expect(result.items[0]).toMatchObject({ clusterId: 'cluster-a', uid: 'k8s:pod-01', type: 'pod', domain: 'kubernetes', namespace: 'payments' })
    expect(result.nextCursor).toBe('next')
    expect(api.get).toHaveBeenCalledWith('/resources/catalog', { params: { domain: 'kubernetes', q: 'api' }, signal: undefined })
  })

  it('maps summary and detail metadata without discarding partial or stale state', async () => {
    vi.spyOn(api, 'get')
      .mockResolvedValueOnce({ data: { domains: [], meta: { generated_at: '2026-09-09T02:00:00Z', partial: true, stale: true, warning_codes: ['RESOURCE_GRAPH_LIMIT'] } } } as never)
      .mockResolvedValueOnce({ data: { data: { uid: 'asset:server-01', cluster_id: 'cluster-a', type: 'physical_server', domain: 'compute', name: 'server-01', location: 'server-01', health: 'degraded', source: 'inventory', capabilities: ['hardware.inspect'] }, meta: { generated_at: '2026-09-09T02:00:00Z', partial: false, stale: false, warning_codes: [] } } } as never)

    const summary = await getResourceSummary()
    const detail = await getResourceDetail('asset:server-01')
    expect(summary.meta).toMatchObject({ partial: true, stale: true, warningCodes: ['RESOURCE_GRAPH_LIMIT'] })
    expect(detail.data).toMatchObject({ clusterId: 'cluster-a', type: 'physical_server', capabilities: ['hardware.inspect'] })
    expect(api.get).toHaveBeenLastCalledWith('/resources/detail', { params: { uid: 'asset%3Aserver-01' }, signal: undefined })
  })

  it('normalizes authorization and backend failures into stable UI kinds', () => {
    expect(toResourceApiError({ response: { status: 403, data: { error: 'PERMISSION_DENIED' } } })).toMatchObject({ kind: 'forbidden', code: 'PERMISSION_DENIED' })
    expect(toResourceApiError({ response: { status: 404, data: { error: 'RESOURCE_NOT_FOUND' } } })).toMatchObject({ kind: 'notFound', code: 'RESOURCE_NOT_FOUND' })
    expect(toResourceApiError({ response: { status: 503, data: { error: 'RESOURCE_CATALOG_UNAVAILABLE' } } })).toMatchObject({ kind: 'unavailable', code: 'RESOURCE_CATALOG_UNAVAILABLE' })
  })
})
