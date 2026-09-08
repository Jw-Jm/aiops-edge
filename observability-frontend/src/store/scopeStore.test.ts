import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getMe, setActiveScope } from '../api/client'
import { useScopeStore } from './scopeStore'
import * as queryClientModule from '../query/client'

vi.mock('../api/client', () => ({
  getMe: vi.fn(),
  setActiveScope: vi.fn(),
}))

const me = (activeClusterId = '', tenantId = 'tenant-a') => ({
  data: {
    user_id: 'user-1',
    session_id: 'session-1',
    active_scope: { tenant_id: tenantId, cluster_id: activeClusterId },
    available_tenants: [tenantId],
    available_clusters: [
      { tenant_id: tenantId, cluster_id: 'cluster-a', name: '生产 A', status: 'ready' },
      { tenant_id: tenantId, cluster_id: 'cluster-b', name: '生产 B', status: 'ready' },
    ],
    capabilities: ['observability.service.read'],
  },
})

describe('scopeStore server-owned active scope', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useScopeStore.setState({
      authScope: null,
      preferredClusterId: '',
      clusters: [],
      loading: false,
      switching: false,
      error: null,
    })
  })

  it('initializes the active scope exclusively from GET /me', async () => {
    vi.mocked(getMe).mockResolvedValue(me('cluster-a') as never)

    await useScopeStore.getState().initialize()

    expect(getMe).toHaveBeenCalledTimes(1)
    expect(useScopeStore.getState().authScope).toEqual({ tenantId: 'tenant-a', activeClusterId: 'cluster-a' })
    expect(useScopeStore.getState().preferredClusterId).toBe('')
  })

  it('does not treat a local preference as the active authorization scope', async () => {
    useScopeStore.setState({ preferredClusterId: 'cluster-b' })
    vi.mocked(getMe).mockResolvedValue(me('') as never)

    await useScopeStore.getState().initialize()

    expect(useScopeStore.getState().authScope).toEqual({ tenantId: 'tenant-a', activeClusterId: '' })
    expect(useScopeStore.getState().isReady()).toBe(false)
  })

  it('switches scope through the server and confirms the resulting projection', async () => {
    const resetQueries = vi.spyOn(queryClientModule, 'resetScopeQueries').mockResolvedValue()
    vi.mocked(setActiveScope).mockResolvedValue({ data: { active_scope: { tenant_id: 'tenant-a', cluster_id: 'cluster-b' } } } as never)
    vi.mocked(getMe).mockResolvedValue(me('cluster-b') as never)
    useScopeStore.setState({ authScope: { tenantId: 'tenant-a', activeClusterId: 'cluster-a' }, clusters: me().data.available_clusters })

    await useScopeStore.getState().switchCluster('cluster-b')

    expect(setActiveScope).toHaveBeenCalledWith('tenant-a', 'cluster-b')
    expect(getMe).toHaveBeenCalledTimes(1)
    expect(useScopeStore.getState().authScope?.activeClusterId).toBe('cluster-b')
    expect(useScopeStore.getState().preferredClusterId).toBe('cluster-b')
    expect(resetQueries).toHaveBeenCalledTimes(1)
    resetQueries.mockRestore()
  })

  it('clears child scope when namespace changes and keeps a selected resource explicit', () => {
    useScopeStore.setState({
      context: {
        environment: 'prod', namespace: 'payment',
        resource: { type: 'service', id: 'payment-api', label: 'payment-api' },
        timeRange: { mode: 'relative', minutes: 60 },
      },
    })
    useScopeStore.getState().setNamespace('checkout')
    expect(useScopeStore.getState().context).toMatchObject({ namespace: 'checkout', resource: undefined })
    useScopeStore.getState().setResource({ type: 'service', id: 'checkout-api', label: 'checkout-api' })
    expect(useScopeStore.getState().context.resource?.id).toBe('checkout-api')
  })
})
