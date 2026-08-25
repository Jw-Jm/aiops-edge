import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { listClusters, type ClusterItem } from '../api/client'

export interface ClusterOption {
  id: number
  cluster_id: string
  name: string
  status: string
  node_count: number
}

interface UIState {
  collapsed: boolean
  aiDockOpen: boolean
  // 多集群纳管：当前选中的集群 id（'all' = 全部集群），持久化
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
      currentClusterId: 'all',
      clusters: [],
      clusterLoading: false,
      toggleCollapsed: () => set((s) => ({ collapsed: !s.collapsed })),
      setAiDockOpen: (v) => set({ aiDockOpen: v }),
      setCurrentCluster: (id) => set({ currentClusterId: id || 'all' }),
      setClusters: (clusters) => set({ clusters }),
      refreshClusters: async () => {
        set({ clusterLoading: true })
        try {
          const res = await listClusters()
          const data = res.data
          const list = Array.isArray(data)
            ? data
            : (data?.data ?? data?.clusters ?? [])
          const options: ClusterOption[] = (list as ClusterItem[]).map((c) => ({
            id: c.id,
            cluster_id: c.cluster_id || '',
            name: c.name,
            status: c.status,
            node_count: c.node_count,
          }))
          set((state) => ({
            clusters: options,
            clusterLoading: false,
            // Clear stale pre-canonical values persisted by older builds. A
            // legacy alias must never become an API authorization context.
            currentClusterId:
              state.currentClusterId === 'all' || options.some((c) => c.cluster_id === state.currentClusterId)
                ? state.currentClusterId
                : 'all',
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
    },
  ),
)
