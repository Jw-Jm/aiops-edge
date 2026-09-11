import { render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import ClusterOverview from './ClusterOverview'
import { getClusterOverview } from '../../api/clusterOverview'

vi.mock('../../api/clusterOverview', () => ({ getClusterOverview: vi.fn() }))

describe('cluster overview', () => {
  beforeEach(() => {
    vi.mocked(getClusterOverview).mockResolvedValue({
      clusterId: 'cluster-a',
      name: '上海集群',
      status: 'degraded',
      statusReason: '采集覆盖不足',
      statusReasons: ['采集覆盖不足'],
      registrationStatus: 'ready',
      stale: false,
      version: 'v1.31.0',
      lastSyncAt: '2026-09-10T10:32:00Z',
      coverage: { covered: 118, expected: 120, ratio: 118 / 120 },
      issues: [],
      resourceKinds: [
        { kind: 'deployment', total: 2, abnormal: 1, unknownOrStale: 0 },
        { kind: 'statefulset', total: 1, abnormal: 0, unknownOrStale: 0 },
        { kind: 'daemonset', total: 1, abnormal: 0, unknownOrStale: 0 },
        { kind: 'job', total: 2, abnormal: 0, unknownOrStale: 0 },
        { kind: 'cronjob', total: 1, abnormal: 0, unknownOrStale: 0 },
        { kind: 'pod', total: 8, abnormal: 1, unknownOrStale: 0 },
        { kind: 'k8s_service', total: 3, abnormal: 0, unknownOrStale: 0 },
        { kind: 'ingress', total: 1, abnormal: 0, unknownOrStale: 0 },
      ],
      kubevirt: { vm: 2, vmi: 2, notReady: 1, migrating: 0, failedMigration: 0, storageAffected: 0, networkAffected: 0 },
      foundation: [
        { kind: 'control_plane', status: 'healthy', reason: 'API Server 可达', affectedResourceCount: 0 },
        { kind: 'nodes_hosts', status: 'degraded', reason: '1 个节点数据陈旧', affectedResourceCount: 1 },
        { kind: 'network', status: 'unknown', reason: '暂无网络面证据', affectedResourceCount: 0 },
        { kind: 'storage', status: 'unknown', reason: '暂无存储面证据', affectedResourceCount: 0 },
        { kind: 'kubevirt', status: 'degraded', reason: '1 个 VMI 未就绪', affectedResourceCount: 1 },
      ],
      meta: { generatedAt: '2026-09-10T10:32:00Z', partial: true, stale: true, warningCodes: ['CLUSTER_DATA_PARTIAL'] },
    })
  })

  it('shows direct carrier panels and foundation evidence', async () => {
    render(<MemoryRouter initialEntries={['/clusters/cluster-a']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Routes><Route path="/clusters/:clusterId" element={<ClusterOverview />} /></Routes></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: '上海集群' })).toBeVisible()
    expect(screen.getByText('容器资源')).toBeVisible()
    expect(screen.getByText('KubeVirt 虚拟机')).toBeVisible()
    expect(screen.getByText('Deployment')).toBeVisible()
    expect(screen.getByText('Kubernetes Service')).toBeVisible()
    expect(screen.getByText('节点与物理机')).toBeVisible()
    expect(screen.getByText('暂无网络面证据')).toBeVisible()
    expect(screen.queryByText('Workload')).not.toBeInTheDocument()
    expect(screen.queryByText('磁盘')).not.toBeInTheDocument()
  })

  it('shows the same observed-health reason and registration status as the platform list', async () => {
    vi.mocked(getClusterOverview).mockResolvedValue({
      clusterId: 'stale-cluster',
      name: 'kind-02',
      status: 'unknown',
      statusReason: '观测数据陈旧',
      statusReasons: ['观测数据陈旧'],
      registrationStatus: 'ready',
      stale: true,
      version: 'v1.31.0',
      lastSyncAt: '2026-09-10T10:32:00Z',
      coverage: { covered: 0, expected: 1, ratio: 0 },
      issues: [],
      resourceKinds: [],
      kubevirt: { vm: 0, vmi: 0, notReady: 0, migrating: 0, failedMigration: 0, storageAffected: 0, networkAffected: 0 },
      foundation: [],
      meta: { generatedAt: '2026-09-10T10:32:00Z', partial: true, stale: true, warningCodes: ['CLUSTER_DATA_STALE'] },
    })
    render(<MemoryRouter initialEntries={['/clusters/stale-cluster']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Routes><Route path="/clusters/:clusterId" element={<ClusterOverview />} /></Routes></MemoryRouter>)

    expect(await screen.findByText('观测数据陈旧')).toBeVisible()
    expect(screen.getByText(/接入：已就绪/)).toBeVisible()
    const statusline = document.querySelector('.cluster-overview-statusline') as HTMLElement
    expect(within(statusline).getByText('未知')).toBeVisible()
    expect(within(statusline).queryByText('健康')).not.toBeInTheDocument()
  })
})
