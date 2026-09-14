import { api } from './client'

/**
 * 集群页运行时事实（设计规范 §6.3）
 *
 * 只呈现服务端真实可验证的值；网络与存储指标未接入时服务端返回
 * quality=not_connected，前端必须原样表达，不得显示 0 或健康。
 */

export interface PodPhaseFact {
  total: number
  running: number
  pending: number
  failed: number
  succeeded: number
  unknown: number
  ready: number
  notReady: number
}

export interface RestartFact {
  totalPodsWithRestarts: number
  totalRestarts: number
  maxPerPod: number
  top: { namespace: string; name: string; restarts: number }[]
  source: string
}

export interface SignalGapFact {
  quality: string
  reason: string
}

export interface ClusterRuntime {
  clusterId: string
  name: string
  pods: PodPhaseFact
  restarts: RestartFact
  network: SignalGapFact
  storage: SignalGapFact
  quality: string
  qualityNote?: string
  generatedAt: string
  source: string
}

interface ClusterRuntimeWire {
  cluster_id: string
  name: string
  pods: { total: number; running: number; pending: number; failed: number; succeeded: number; unknown: number; not_ready: number; ready: number }
  restarts: { total_pods_with_restarts: number; total_restarts: number; max_per_pod: number; top: { namespace: string; name: string; restarts: number }[]; source: string }
  network: { quality: string; reason: string }
  storage: { quality: string; reason: string }
  quality: string
  quality_note?: string
  generated_at: string
  source: string
}

export async function getClusterRuntime(clusterId: string, signal?: AbortSignal): Promise<ClusterRuntime> {
  const res = await api.get<ClusterRuntimeWire>(`/clusters/${encodeURIComponent(clusterId)}/runtime`, { signal })
  const d = res.data
  return {
    clusterId: d.cluster_id,
    name: d.name,
    pods: {
      total: d.pods?.total ?? 0,
      running: d.pods?.running ?? 0,
      pending: d.pods?.pending ?? 0,
      failed: d.pods?.failed ?? 0,
      succeeded: d.pods?.succeeded ?? 0,
      unknown: d.pods?.unknown ?? 0,
      ready: d.pods?.ready ?? 0,
      notReady: d.pods?.not_ready ?? 0,
    },
    restarts: {
      totalPodsWithRestarts: d.restarts?.total_pods_with_restarts ?? 0,
      totalRestarts: d.restarts?.total_restarts ?? 0,
      maxPerPod: d.restarts?.max_per_pod ?? 0,
      top: d.restarts?.top ?? [],
      source: d.restarts?.source ?? '',
    },
    network: { quality: d.network?.quality ?? 'unknown', reason: d.network?.reason ?? '' },
    storage: { quality: d.storage?.quality ?? 'unknown', reason: d.storage?.reason ?? '' },
    quality: d.quality,
    ...(d.quality_note ? { qualityNote: d.quality_note } : {}),
    generatedAt: d.generated_at,
    source: d.source,
  }
}
