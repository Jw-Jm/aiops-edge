import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getMe, setActiveScope } from '../api/client'
import { setScopeCluster } from '../api/scopeRuntime'
import { resetScopeQueries } from '../query/client'
import { DEFAULT_SCOPE_CONTEXT, type Environment, type ResourceRef, type ScopeContext, type TimeRange } from '../features/scope/types'

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
  context: ScopeContext
  preferredClusterId: string
  clusters: ScopeCluster[]
  loading: boolean
  switching: boolean
  error: string | null
  initialize: () => Promise<void>
  switchCluster: (clusterId: string) => Promise<void>
  setPreferredCluster: (clusterId: string) => void
  setEnvironment: (environment: Environment) => void
  setNamespace: (namespace: string) => void
  setResource: (resource?: ResourceRef) => void
  setTimeRange: (timeRange: TimeRange) => void
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

export const useScopeStore = create<ScopeState>()(
  persist(
    (set, get) => ({
      authScope: null,
      context: DEFAULT_SCOPE_CONTEXT,
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
          set({
            authScope: nextScope,
            clusters: projectClusters(response.data.available_clusters),
            context: get().context,
            loading: false,
          })
        } catch (error) {
          set({ loading: false, error: error instanceof Error ? error.message : 'Scope 初始化失败' })
          throw error
        }
      },
      switchCluster: async (clusterId) => {
        const target = get().clusters.find((cluster) => cluster.cluster_id === clusterId)
        if (!target) throw new Error('未找到可授权集群')
        const previousScope = get().authScope
        if (previousScope?.activeClusterId === clusterId) {
          set({ preferredClusterId: clusterId })
          return
        }
        await resetScopeQueries()
        set({ switching: true, error: null, authScope: previousScope ? { ...previousScope, activeClusterId: '' } : null })
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
            preferredClusterId: confirmed.activeClusterId,
            clusters: projectClusters(response.data.available_clusters),
            context: { ...get().context, namespace: '', resource: undefined },
            switching: false,
          })
        } catch (error) {
          setScopeCluster(previousScope?.activeClusterId ?? '')
          set({
            authScope: previousScope,
            switching: false,
            error: error instanceof Error ? error.message : 'Scope 切换失败',
          })
          throw error
        }
      },
      setPreferredCluster: (clusterId) => set({ preferredClusterId: clusterId }),
      setEnvironment: (environment) => set((state) => ({ context: { ...state.context, environment } })),
      setNamespace: (namespace) => set((state) => ({ context: { ...state.context, namespace, resource: undefined } })),
      setResource: (resource) => set((state) => ({ context: { ...state.context, resource } })),
      setTimeRange: (timeRange) => set((state) => ({ context: { ...state.context, timeRange } })),
      isReady: () => Boolean(get().authScope?.tenantId && get().authScope?.activeClusterId),
    }),
    {
      name: 'aiops-scope-preference',
      partialize: (state) => ({ preferredClusterId: state.preferredClusterId }),
    },
  ),
)
