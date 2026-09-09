import { describe, expect, it } from 'vitest'
import { queryKeys } from './keys'

describe('scope-isolated query keys', () => {
  it('includes tenant and active cluster in every cluster-scoped key', () => {
    expect(queryKeys.services({ tenantId: 'tenant-a', activeClusterId: 'cluster-a' })).toEqual([
      'services', 'tenant-a', 'cluster-a', '-', '-', '-', '-',
    ])
  })

  it('keeps resource filters and graph mode isolated within the active scope', () => {
    const context = { tenantId: 'tenant-a', activeClusterId: 'cluster-a', entityUid: 'pod-1', from: '2026-09-09T01:00:00Z', to: '2026-09-09T02:00:00Z' }
    expect(queryKeys.resourceCatalog(context, { q: 'api', domain: 'kubernetes' })).toEqual(queryKeys.resourceCatalog(context, { domain: 'kubernetes', q: 'api' }))
    expect(queryKeys.resourceGraph(context, 'pod-1', 'failure-chain', 1, ['kubernetes', 'compute'], ['HOSTS', 'DEPENDS_ON'])).toEqual([
      'resource-graph', 'pod-1', 'failure-chain', 1, 'compute,kubernetes', 'DEPENDS_ON,HOSTS', 'tenant-a', 'cluster-a', '-', 'pod-1', '2026-09-09T01:00:00Z', '2026-09-09T02:00:00Z',
    ])
  })
})
