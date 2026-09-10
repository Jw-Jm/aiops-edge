import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Overview from './index'
import { getPlatformClusters, getPlatformOverview } from '../../api/platform'

vi.mock('../../api/platform', () => ({
  getPlatformClusters: vi.fn(),
  getPlatformOverview: vi.fn(),
}))

describe('platform overview', () => {
  beforeEach(() => {
    vi.mocked(getPlatformOverview).mockResolvedValue({
      activeCriticalIssues: 2,
      managedClusters: 4,
      affectedClusters: 1,
      unknownOrStaleClusters: 1,
      clusterStates: { healthy: 2, degraded: 1, critical: 1, unknown: 0 },
      coverage: { covered: 118, expected: 120, ratio: 118 / 120 },
      capabilitySummary: { healthy: 6, total: 7, issues: ['拓扑同步延迟'] },
      meta: { generatedAt: '2026-09-10T10:32:00Z', partial: false, stale: false, warningCodes: [] },
    })
    vi.mocked(getPlatformClusters).mockResolvedValue({
      clusters: [{ clusterId: 'cluster-a', name: '上海集群', status: 'critical', updatedAt: '2026-09-10T10:32:00Z' }],
      count: 1,
      total: 1,
      meta: { generatedAt: '2026-09-10T10:32:00Z', partial: false, stale: false, warningCodes: [] },
    })
  })

  it('shows platform facts and direct resource carrier labels', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)
    expect(await screen.findByText('当前活动严重问题')).toBeVisible()
    expect(screen.getByText('受影响集群')).toBeVisible()
    expect(screen.getByText('Deployment')).toBeVisible()
    expect(screen.getByText('StatefulSet')).toBeVisible()
    expect(screen.getByText('DaemonSet')).toBeVisible()
    expect(screen.getByText('Job')).toBeVisible()
    expect(screen.getByText('CronJob')).toBeVisible()
    expect(screen.getByText('Pod')).toBeVisible()
    expect(screen.getByText('Kubernetes Service')).toBeVisible()
    expect(screen.getByText('Ingress')).toBeVisible()
    expect(screen.queryByText('被纳管云平台状态')).not.toBeInTheDocument()
    expect(screen.queryByText(/综合分数|健康率/)).not.toBeInTheDocument()
    expect(screen.queryByText('Workload')).not.toBeInTheDocument()
  })

  it('keeps the aggregate independent of the active cluster scope', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)
    await screen.findByText('当前活动严重问题')
    expect(getPlatformOverview).toHaveBeenCalledWith(expect.any(AbortSignal))
    expect(getPlatformOverview).toHaveBeenCalledTimes(1)
  })
})
