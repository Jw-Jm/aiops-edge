import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getMe, setActiveScope } from '../api/client'
import { setScopeCluster } from '../api/scopeRuntime'
import { resetScopeQueries } from '../query/client'
import { DEFAULT_ACTIVE_SCOPE, type ActiveScope, type RunScopeSnapshot, type TimeRange } from '../features/scope/types'
import type { PlatformResourceRef } from '../features/resources/types'

export interface AuthScope {
  tenantId: string
  activeClusterId: string
}

export interface ScopeCluster {
  cluster_id: string
  tenant_id: string
  name: string
  status: string
  node_count?: number
}

interface ScopeState {
  authScope: AuthScope | null
  capabilities: string[]
  active: ActiveScope
  preferredClusterId: string
  clusters: ScopeCluster[]
  loading: boolean
  switching: boolean
  error: string | null
  initialize: () => Promise<void>
  switchCluster: (clusterId: string) => Promise<void>
  setPreferredCluster: (clusterId: string) => void
  setResource: (resource?: PlatformResourceRef) => void
  setTimeRange: (timeRange: TimeRange) => void
  snapshotForRun: (runId: string, window: Extract<TimeRange, { mode: 'absolute' }>) => RunScopeSnapshot
  isReady: () => boolean
}

function projectScope(data: { tenant_id?: string; cluster_id?: string } | null | undefined): AuthScope {
  return { tenantId: data?.tenant_id ?? '', activeClusterId: data?.cluster_id ?? '' }
}

function projectClusters(data: Array<{ cluster_id: string; tenant_id?: string; name: string; status?: string; node_count?: number }> | undefined): ScopeCluster[] {
  return (data ?? [])
    .filter((cluster) => Boolean(cluster.cluster_id && cluster.tenant_id))
    .map((cluster) => ({
      cluster_id: cluster.cluster_id,
      tenant_id: cluster.tenant_id ?? '',
      name: cluster.name,
      status: cluster.status ?? 'unknown',
      node_count: cluster.node_count,
    }))
}

function activeScopeFromAuth(authScope: AuthScope, previous: ActiveScope): ActiveScope {
  return {
    tenantId: authScope.tenantId,
    clusterId: authScope.activeClusterId,
    ...(previous.clusterId === authScope.activeClusterId && previous.resource?.clusterId === authScope.activeClusterId ? { resource: previous.resource } : {}),
    timeRange: previous.timeRange,
  }
}

export const useScopeStore = create<ScopeState>()(
  persist(
    (set, get) => ({
      authScope: null,
      capabilities: [],
      active: DEFAULT_ACTIVE_SCOPE,
      preferredClusterId: '',
      clusters: [],
      loading: false,
      switching: false,
      error: null,
      initialize: async () => {
        set({ loading: true, error: null })
        try {
          const response = await getMe()
          const nextScope = projectScope(response.data.active_scope)
          setScopeCluster(nextScope.activeClusterId)
          set((state) => ({
            authScope: nextScope,
            capabilities: Array.isArray(response.data.capabilities) ? response.data.capabilities : [],
            active: activeScopeFromAuth(nextScope, state.active),
            clusters: projectClusters(response.data.available_clusters),
            loading: false,
          }))
        } catch (error) {
          set({ loading: false, error: error instanceof Error ? error.message : 'Scope 初始化失败' })
          throw error
        }
      },
      switchCluster: async (clusterId) => {
        const target = get().clusters.find((cluster) => cluster.cluster_id === clusterId)
        if (!target) throw new Error('未找到可授权集群')
        const previousScope = get().authScope
        const previousActive = get().active
        if (previousScope?.activeClusterId === clusterId) {
          set({ preferredClusterId: clusterId })
          return
        }
        await resetScopeQueries()
        set({ switching: true, error: null, authScope: previousScope ? { ...previousScope, activeClusterId: '' } : null, active: { ...previousActive, clusterId: '', resource: undefined } })
        setScopeCluster('')
        try {
          await setActiveScope(target.tenant_id, target.cluster_id)
          const response = await getMe()
          const confirmed = projectScope(response.data.active_scope)
          if (confirmed.tenantId !== target.tenant_id || confirmed.activeClusterId !== target.cluster_id) {
            throw new Error('服务端未确认新的 active scope')
          }
          setScopeCluster(confirmed.activeClusterId)
          set({
            authScope: confirmed,
            capabilities: Array.isArray(response.data.capabilities) ? response.data.capabilities : get().capabilities,
            active: { tenantId: confirmed.tenantId, clusterId: confirmed.activeClusterId, timeRange: previousActive.timeRange },
            preferredClusterId: confirmed.activeClusterId,
            clusters: projectClusters(response.data.available_clusters),
            switching: false,
          })
        } catch (error) {
          setScopeCluster(previousScope?.activeClusterId ?? '')
          set({ authScope: previousScope, active: previousActive, switching: false, error: error instanceof Error ? error.message : 'Scope 切换失败' })
          throw error
        }
      },
      setPreferredCluster: (clusterId) => set({ preferredClusterId: clusterId }),
      setResource: (resource) => {
        const activeClusterId = get().active.clusterId || get().authScope?.activeClusterId || ''
        if (resource && resource.clusterId !== activeClusterId) throw new Error('跨集群资源不能进入当前范围')
        set((state) => ({ active: { ...state.active, resource } }))
      },
      setTimeRange: (timeRange) => set((state) => ({ active: { ...state.active, timeRange } })),
      snapshotForRun: (runId, window) => {
        const active = get().active
        return { ...active, mode: 'snapshot', runId, timeRange: window }
      },
      isReady: () => Boolean(get().active.tenantId && get().active.clusterId),
    }),
    {
      name: 'aiops-scope-preference',
      partialize: (state) => ({ preferredClusterId: state.preferredClusterId }),
    },
  ),
)
