import { describe, expect, it } from 'vitest'
import type { ActiveScope } from './types'
import { toLegacyRunScope } from './runScopeAdapter'

describe('toLegacyRunScope', () => {
  it('fixes legacy environment to prod and projects namespace only from Kubernetes resources', () => {
    const scope: ActiveScope = {
      tenantId: 'tenant-a', clusterId: 'cluster-a',
      resource: { clusterId: 'cluster-a', uid: 'pod:api', type: 'pod', domain: 'kubernetes', name: 'api', namespace: 'payments' },
      timeRange: { mode: 'relative', minutes: 60 },
    }
    expect(toLegacyRunScope(scope, new Date('2026-09-09T02:00:00.000Z'))).toEqual({
      environment: 'prod', cluster_id: 'cluster-a', namespace: 'payments', target_resource_id: 'pod:api', target_resource_type: 'pod',
      time_start: '2026-09-09T01:00:00.000Z', time_end: '2026-09-09T02:00:00.000Z',
    })
  })

  it('does not invent namespace for physical resources and preserves absolute windows', () => {
    const scope: ActiveScope = {
      tenantId: 'tenant-a', clusterId: 'cluster-a',
      resource: { clusterId: 'cluster-a', uid: 'server:01', type: 'physical_server', domain: 'compute', name: 'server-01' },
      timeRange: { mode: 'absolute', start: '2026-09-09T00:00:00.000Z', end: '2026-09-09T00:30:00.000Z' },
    }
    expect(toLegacyRunScope(scope, new Date('2026-09-09T02:00:00.000Z'))).toMatchObject({
      environment: 'prod', cluster_id: 'cluster-a', target_resource_id: 'server:01', target_resource_type: 'physical_server',
      time_start: '2026-09-09T00:00:00.000Z', time_end: '2026-09-09T00:30:00.000Z',
    })
    expect(toLegacyRunScope(scope, new Date('2026-09-09T02:00:00.000Z'))).not.toHaveProperty('namespace')
  })
})
