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
      capabilities: [],
      preferredClusterId: '',
      clusters: [],
      loading: false,
      switching: false,
      error: null,
      active: { tenantId: '', clusterId: '', timeRange: { mode: 'relative', minutes: 60 } },
    })
  })

  it('initializes the active scope exclusively from GET /me', async () => {
    vi.mocked(getMe).mockResolvedValue(me('cluster-a') as never)

    await useScopeStore.getState().initialize()

    expect(getMe).toHaveBeenCalledTimes(1)
    expect(useScopeStore.getState().authScope).toEqual({ tenantId: 'tenant-a', activeClusterId: 'cluster-a' })
    expect(useScopeStore.getState().capabilities).toEqual(['observability.service.read'])
    expect(useScopeStore.getState().preferredClusterId).toBe('')
  })

  it('does not treat a local preference as the active authorization scope', async () => {
    useScopeStore.setState({ preferredClusterId: 'cluster-b' })
    vi.mocked(getMe).mockResolvedValue(me('') as never)

    await useScopeStore.getState().initialize()

    expect(useScopeStore.getState().authScope).toEqual({ tenantId: 'tenant-a', activeClusterId: '' })
    expect(useScopeStore.getState().isReady()).toBe(false)
  })

  it('在新会话服务端 active_scope 为空时主动建立被服务端确认的范围', async () => {
    // 回归缺陷：新会话下服务端 active_scope 为空，所有集群级只读请求被 fail-closed 403，
    // 页面表现为"接口失败"。初始化必须主动选择可用集群并由服务端确认。
    useScopeStore.setState({ preferredClusterId: 'cluster-b' })
    vi.mocked(setActiveScope).mockResolvedValue({ data: { active_scope: { tenant_id: 'tenant-a', cluster_id: 'cluster-b' } } } as never)
    vi.mocked(getMe).mockResolvedValueOnce(me('') as never).mockResolvedValue(me('cluster-b') as never)

    await useScopeStore.getState().initialize()

    expect(setActiveScope).toHaveBeenCalledWith('tenant-a', 'cluster-b')
    expect(getMe).toHaveBeenCalledTimes(2)
    expect(useScopeStore.getState().authScope).toEqual({ tenantId: 'tenant-a', activeClusterId: 'cluster-b' })
    expect(useScopeStore.getState().isReady()).toBe(true)
  })

  it('没有记住偏好时选择第一个可用集群并确认', async () => {
    vi.mocked(setActiveScope).mockResolvedValue({ data: { active_scope: { tenant_id: 'tenant-a', cluster_id: 'cluster-a' } } } as never)
    vi.mocked(getMe).mockResolvedValueOnce(me('') as never).mockResolvedValue(me('cluster-a') as never)

    await useScopeStore.getState().initialize()

    expect(setActiveScope).toHaveBeenCalledWith('tenant-a', 'cluster-a')
    expect(useScopeStore.getState().authScope?.activeClusterId).toBe('cluster-a')
  })

  it('建立范围失败时保持未选择并按真实状态暴露', async () => {
    vi.mocked(setActiveScope).mockRejectedValue(new Error('server rejected scope'))
    vi.mocked(getMe).mockResolvedValue(me('') as never)

    await useScopeStore.getState().initialize()

    expect(useScopeStore.getState().authScope?.activeClusterId).toBe('')
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

  it('clears the selected resource when the confirmed cluster changes', async () => {
    useScopeStore.setState({
      authScope: { tenantId: 'tenant-a', activeClusterId: 'cluster-a' },
      active: { tenantId: 'tenant-a', clusterId: 'cluster-a', resource: { clusterId: 'cluster-a', uid: 'service:payment-api', type: 'service', domain: 'application', name: 'payment-api' }, timeRange: { mode: 'relative', minutes: 60 } },
      clusters: me().data.available_clusters,
    })
    vi.mocked(setActiveScope).mockResolvedValue({ data: { active_scope: { tenant_id: 'tenant-a', cluster_id: 'cluster-b' } } } as never)
    vi.mocked(getMe).mockResolvedValue(me('cluster-b') as never)

    await useScopeStore.getState().switchCluster('cluster-b')

    expect(useScopeStore.getState().active).toMatchObject({ tenantId: 'tenant-a', clusterId: 'cluster-b' })
    expect(useScopeStore.getState().active).not.toHaveProperty('resource')
  })

  it('rejects a resource from another cluster and keeps namespace out of global scope', () => {
    useScopeStore.setState({
      authScope: { tenantId: 'tenant-a', activeClusterId: 'cluster-a' },
      active: { tenantId: 'tenant-a', clusterId: 'cluster-a', timeRange: { mode: 'relative', minutes: 60 } },
    })
    expect(() => useScopeStore.getState().setResource({ clusterId: 'cluster-b', uid: 'pod:b', type: 'pod', domain: 'kubernetes', name: 'api' })).toThrow('跨集群资源')
    expect('setNamespace' in useScopeStore.getState()).toBe(false)
    expect('setEnvironment' in useScopeStore.getState()).toBe(false)
  })
})
