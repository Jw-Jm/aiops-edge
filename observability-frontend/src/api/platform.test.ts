import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { getPlatformClusters, getPlatformOverview, mapPlatformOverview } from './platform'

afterEach(() => vi.restoreAllMocks())

describe('platform API client', () => {
  it('maps platform facts without inventing a synthetic platform status', () => {
    const result = mapPlatformOverview({
      active_cluster_id: 'cluster-a',
      active_critical_issues: 3,
      managed_clusters: 12,
      affected_clusters: 2,
      unknown_or_stale_clusters: 1,
      cluster_states: { healthy: 8, degraded: 2, critical: 1, unknown: 1 },
      coverage: { covered: 118, expected: 120, ratio: 118 / 120 },
      freshest_at: '2026-09-10T10:32:00Z',
      oldest_valid_at: '2026-09-10T10:25:00Z',
      highest_priority: { cluster_id: 'cluster-b', resource_uid: 'pod:api', rule_id: 'pod-not-ready', severity: 'critical', status: 'firing', title: 'Pod 未就绪', observed_at: '2026-09-10T10:30:00Z' },
      capability_summary: { healthy: 6, total: 7, issues: ['拓扑同步延迟'] },
      meta: { generated_at: '2026-09-10T10:32:00Z', partial: true, stale: true, warning_codes: ['CLUSTER_DATA_STALE'] },
    })

    expect(result).toMatchObject({
      activeClusterId: 'cluster-a',
      managedClusters: 12,
      clusterStates: { healthy: 8, degraded: 2, critical: 1, unknown: 1 },
      coverage: { covered: 118, expected: 120 },
      highestPriority: { clusterId: 'cluster-b', resourceUid: 'pod:api' },
      meta: { partial: true, stale: true, warningCodes: ['CLUSTER_DATA_STALE'] },
    })
    expect(result).not.toHaveProperty('platformStatus')
  })

  it('keeps observed health and registration status separate on cluster rows', async () => {
    vi.spyOn(api, 'get').mockResolvedValueOnce({ data: {
      clusters: [{
        cluster_id: 'cluster-a', name: 'A', status: 'unknown', registration_status: 'ready',
        status_reason: '观测数据陈旧', stale: true, covered: false, updated_at: '2026-09-10T10:32:00Z',
      }],
      count: 1, total: 1, meta: { generated_at: 'now', partial: true, stale: true, warning_codes: ['CLUSTER_DATA_STALE'] },
    } } as never)

    const result = await getPlatformClusters()
    expect(result.clusters[0]).toMatchObject({
      clusterId: 'cluster-a',
      status: 'unknown',
      registrationStatus: 'ready',
      statusReason: '观测数据陈旧',
      stale: true,
      covered: false,
    })
    // 注册状态不能被当成健康结论。
    expect(result.clusters[0].status).not.toBe('healthy')
  })

  it('uses global platform endpoints without adding an active cluster filter', async () => {
    vi.spyOn(api, 'get')
      .mockResolvedValueOnce({ data: { active_critical_issues: 0, managed_clusters: 0, affected_clusters: 0, unknown_or_stale_clusters: 0, cluster_states: {}, coverage: { covered: 0, expected: 0 }, capability_summary: {}, meta: { generated_at: 'now', partial: false, stale: false, warning_codes: [] } } } as never)
      .mockResolvedValueOnce({ data: { clusters: [{ cluster_id: 'cluster-a', name: 'A', status: 'healthy', updated_at: 'now' }], count: 1, total: 1, meta: { generated_at: 'now', partial: false, stale: false, warning_codes: [] } } } as never)

    await getPlatformOverview()
    await getPlatformClusters({ status: 'healthy', limit: 20 })
    expect(api.get).toHaveBeenNthCalledWith(1, '/platform/overview', { signal: undefined })
    expect(api.get).toHaveBeenNthCalledWith(2, '/platform/clusters', { params: { status: 'healthy', limit: 20 }, signal: undefined })
  })
})
