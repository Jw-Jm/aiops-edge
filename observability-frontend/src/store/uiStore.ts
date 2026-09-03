import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getMe } from '../api/client'
import { setScopeCluster } from '../api/scopeRuntime'

export interface ClusterOption {
  id: number
  cluster_id: string
  tenant_id?: string
  name: string
  status: string
  node_count: number
}

interface UIState {
  collapsed: boolean
  aiDockOpen: boolean
  // 多集群纳管：当前选中的集群 id；空值表示尚未选择作用域。
  currentClusterId: string
  clusters: ClusterOption[]
  // 集群是否加载中/失败
  clusterLoading: boolean
  toggleCollapsed: () => void
  setAiDockOpen: (v: boolean) => void
  setCurrentCluster: (id: string) => void
  setClusters: (clusters: ClusterOption[]) => void
  refreshClusters: () => Promise<void>
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      collapsed: false,
      aiDockOpen: false,
      currentClusterId: '',
      clusters: [],
      clusterLoading: false,
      toggleCollapsed: () => set((s) => ({ collapsed: !s.collapsed })),
      setAiDockOpen: (v) => set({ aiDockOpen: v }),
      setCurrentCluster: (id) => {
        const value = id || ''
        set({ currentClusterId: value })
        // P2-A2: 同步到请求拦截器使用的内存 scope runtime
        setScopeCluster(value)
      },
      setClusters: (clusters) => set({ clusters }),
      refreshClusters: async () => {
        set({ clusterLoading: true })
        try {
          const res = await getMe()
          const list = res.data?.available_clusters ?? []
          const options: ClusterOption[] = list.map((c, index) => ({
            id: index,
            tenant_id: c.tenant_id,
            cluster_id: c.cluster_id || '',
            name: c.name,
            status: c.status || 'unknown',
            node_count: 0,
          }))
          set((state) => ({
            clusters: options,
            clusterLoading: false,
            // Clear stale pre-canonical values persisted by older builds. A
            // legacy alias must never become an API authorization context.
            currentClusterId:
              options.some((c) => c.cluster_id === state.currentClusterId)
                ? state.currentClusterId
                : '',
          }))
        } catch (e) {
          // 集群接口失败不阻塞页面：保持已缓存列表，仅清 loading
          set({ clusterLoading: false })
          void e
        }
      },
    }),
    {
      name: 'aiops-ui-v3',
      // 仅持久化用户选择项，不持久化动态集群列表
      partialize: (s) => ({
        collapsed: s.collapsed,
        aiDockOpen: s.aiDockOpen,
        currentClusterId: s.currentClusterId,
      }),
      // P2-A2: hydrate 完成后同步 scope runtime（拦截器不再逐请求读 localStorage）
      onRehydrateStorage: () => (state) => {
        setScopeCluster(state?.currentClusterId ?? '')
      },
    },
  ),
)
