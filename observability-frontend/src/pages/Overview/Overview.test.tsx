import { render, screen, within } from '@testing-library/react'
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
      clusters: [{ clusterId: 'cluster-a', name: '上海集群', status: 'critical', registrationStatus: 'ready', statusReason: '观测到严重异常', stale: false, covered: true, updatedAt: '2026-09-10T10:32:00Z' }],
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

  it('shows the observed-health reason and separates registration status per cluster', async () => {
    vi.mocked(getPlatformClusters).mockResolvedValue({
      clusters: [{
        clusterId: 'cluster-a', name: '上海集群', status: 'unknown',
        registrationStatus: 'ready', statusReason: '观测数据陈旧', stale: true, covered: false,
        updatedAt: '2026-09-10T10:32:00Z',
      }],
      count: 1,
      total: 1,
      meta: { generatedAt: '2026-09-10T10:32:00Z', partial: true, stale: true, warningCodes: [] },
    })
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)

    const row = await screen.findByRole('button', { name: /上海集群/ })
    expect(within(row).getByText('未知')).toBeVisible()
    expect(within(row).getByText('观测数据陈旧')).toBeVisible()
    expect(within(row).getByText(/接入：已就绪/)).toBeVisible()
    // 注册 ready 不能被呈现为健康。
    expect(within(row).queryByText('健康')).not.toBeInTheDocument()
  })

  it('does not fabricate a healthy capability ratio when no probe result exists', async () => {
    vi.mocked(getPlatformOverview).mockResolvedValue({
      activeCriticalIssues: 0,
      managedClusters: 1,
      affectedClusters: 0,
      unknownOrStaleClusters: 0,
      clusterStates: { healthy: 0, degraded: 0, critical: 0, unknown: 1 },
      coverage: { covered: 0, expected: 1, ratio: 0 },
      capabilitySummary: { healthy: 0, total: 0, issues: [] },
      meta: { generatedAt: '2026-09-10T10:32:00Z', partial: true, stale: false, warningCodes: [] },
    })
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)

    expect(await screen.findByText('未获得组件状态')).toBeVisible()
    expect(screen.queryByText('0/0')).not.toBeInTheDocument()
    expect(screen.queryByText('不可用')).not.toBeInTheDocument()
  })
})
