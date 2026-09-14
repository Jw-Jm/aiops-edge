import { render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Overview from './index'
import { getPlatformCapacity, getPlatformClusters, getPlatformOverview } from '../../api/platform'

vi.mock('../../api/platform', () => ({
  getPlatformClusters: vi.fn(),
  getPlatformOverview: vi.fn(),
  getPlatformCapacity: vi.fn(),
}))

const resourceFact = (over: Partial<Record<string, unknown>> = {}) => ({
  used: 7.9,
  allocatable: 16,
  usageRatio: 7.9 / 16,
  unit: 'cores',
  aggregation: 'sum(node usage) / sum(node allocatable)',
  source: 'metrics.k8s.io/v1beta1 + core/v1 nodes.allocatable',
  sourceTimestamp: '2026-09-13T06:40:00Z',
  ...over,
})

describe('platform overview (V1.4 §6.2)', () => {
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
    vi.mocked(getPlatformCapacity).mockResolvedValue({
      clusters: [
        {
          clusterId: 'cluster-a',
          name: '上海集群',
          cpu: resourceFact(),
          memory: resourceFact({ used: 8 * 1024 ** 3, allocatable: 32 * 1024 ** 3, usageRatio: 0.25, unit: 'bytes' }),
          nodes: { total: 3, ready: 2, notReady: 1, unknown: 0 },
          p95CpuUtilization: 92.5,
          p95MemUtilization: 61.2,
          maxCpuUtilization: 97.5,
          maxMemUtilization: 70,
          hotNodeCount: 1,
          hotNodeThresholdPct: 80,
          quality: 'healthy',
          registrationStatus: 'active',
        },
      ],
      count: 1,
      meta: { generatedAt: '2026-09-13T06:40:00Z', partial: false, stale: false, warningCodes: [] },
    })
  })

  it('shows the three spec first-screen facts and no low-value resource carrier cards', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)
    expect(await screen.findByText('活动严重问题')).toBeVisible()
    expect(screen.getByText('受影响集群')).toBeVisible()
    expect(screen.getByText('采集覆盖')).toBeVisible()

    // §6.2 禁止项：不显示资源类型说明卡，不重复 warning code，不使用健康分数
    expect(screen.queryByText('平台关注的资源载体')).not.toBeInTheDocument()
    expect(screen.queryByText('Deployment')).not.toBeInTheDocument()
    expect(screen.queryByText('StatefulSet')).not.toBeInTheDocument()
    expect(screen.queryByText(/综合分数|健康率|0-100/)).not.toBeInTheDocument()
    expect(screen.queryByText('综合健康分数')).not.toBeInTheDocument()
  })

  it('binds every cluster to a cluster-scope CPU/memory fact with unit, aggregation and source', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)
    const table = await screen.findByRole('table', { name: '' }).catch(() => null)
    void table

    // 集群口径：已用/可分配，单位显式
    expect(await screen.findByText('7.90 核 / 16.00 核')).toBeVisible()
    expect(screen.getByText('8.0 GiB / 32.0 GiB')).toBeVisible()
    // 平均值不得掩盖单节点热点：P95/最大与热点节点数必须可见
    expect(screen.getByText(/92\.5% \/ 97\.5%/)).toBeVisible()
    expect(screen.getAllByText('1 个').length).toBeGreaterThan(0)
    // 节点就绪包含未就绪明细
    expect(screen.getByText(/2\/3（未就绪 1）/)).toBeVisible()
    // 口径与来源必须声明
    expect(screen.getByText(/CPU 已用核数 ÷ 可分配核数/)).toBeVisible()
  })

  it('keeps the aggregate independent of the active cluster scope', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)
    await screen.findByText('活动严重问题')
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

    // 容量表与集群清单都会渲染集群名，这里精确定位到"纳管集群清单"的整行按钮。
    const rows = await screen.findAllByRole('button', { name: /上海集群/ })
    const found = rows.find((el) => el.className.includes('platform-cluster-row'))
    if (!found) throw new Error('纳管集群清单行未渲染')
    const row: HTMLElement = found
    expect(within(row).getByText('未知')).toBeVisible()
    expect(within(row).getByText('观测数据陈旧')).toBeVisible()
    expect(within(row).getByText(/接入：已就绪/)).toBeVisible()
    // 注册 ready 不能被呈现为健康。
    expect(within(row).queryByText('健康')).not.toBeInTheDocument()
  })

  it('renders 未接入集群 as not connected instead of 0 or healthy', async () => {
    vi.mocked(getPlatformCapacity).mockResolvedValue({
      clusters: [{
        clusterId: 'cluster-b',
        name: '异地集群',
        cpu: resourceFact({ used: 0, allocatable: 0, usageRatio: null }),
        memory: resourceFact({ used: 0, allocatable: 0, usageRatio: null, unit: 'bytes' }),
        nodes: { total: 0, ready: 0, notReady: 0, unknown: 0 },
        p95CpuUtilization: null,
        p95MemUtilization: null,
        maxCpuUtilization: null,
        maxMemUtilization: null,
        hotNodeCount: 0,
        hotNodeThresholdPct: 80,
        quality: 'not_connected',
        qualityReason: '该集群未接入中央指标读取通道',
        registrationStatus: 'ready',
      }],
      count: 1,
      meta: { generatedAt: '2026-09-13T06:40:00Z', partial: true, stale: false, warningCodes: ['cluster cluster-b capacity not connected'] },
    })
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)

    expect(await screen.findByText('未接入')).toBeVisible()
    expect(screen.getAllByText('未提供').length).toBeGreaterThan(0)
    // 未接入不得被渲染成 0% 的绿色健康
    expect(screen.queryByText('0.0%')).not.toBeInTheDocument()
    expect(screen.queryByText('数据完整')).not.toBeInTheDocument()
  })

  it('shows at most one data-trust banner and does not duplicate warning codes', async () => {
    vi.mocked(getPlatformOverview).mockResolvedValue({
      activeCriticalIssues: 0,
      managedClusters: 2,
      affectedClusters: 0,
      unknownOrStaleClusters: 1,
      clusterStates: { healthy: 1, degraded: 0, critical: 0, unknown: 1 },
      coverage: { covered: 1, expected: 2, ratio: 0.5 },
      capabilitySummary: { healthy: 3, total: 3, issues: [] },
      meta: { generatedAt: '2026-09-13T06:40:00Z', partial: true, stale: false, warningCodes: ['graph_partial', 'vlogs_stale'] },
    })
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Overview /></MemoryRouter>)

    const banners = await screen.findAllByTestId('data-trust-banner')
    expect(banners).toHaveLength(1)
    // warning code 只进入技术详情，不作为普通用户文案
    expect(screen.queryByText('graph_partial')).not.toBeInTheDocument()
    expect(screen.queryByText('vlogs_stale')).not.toBeInTheDocument()
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
