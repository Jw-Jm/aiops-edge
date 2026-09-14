import { render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import ClusterOverview from './ClusterOverview'
import { getClusterOverview } from '../../api/clusterOverview'
import { getClusterRuntime } from '../../api/clusterRuntime'
import { getPlatformCapacity } from '../../api/platform'

vi.mock('../../api/clusterOverview', () => ({ getClusterOverview: vi.fn() }))
vi.mock('../../api/clusterRuntime', () => ({ getClusterRuntime: vi.fn() }))
vi.mock('../../api/platform', () => ({ getPlatformCapacity: vi.fn() }))

const notConnectedRuntime = {
  clusterId: 'cluster-a',
  name: '上海集群',
  pods: { total: 0, running: 0, pending: 0, failed: 0, succeeded: 0, unknown: 0, ready: 0, notReady: 0 },
  restarts: { totalPodsWithRestarts: 0, totalRestarts: 0, maxPerPod: 0, top: [], source: '' },
  network: { quality: 'not_connected', reason: '未接入网络吞吐/错误/丢包/时延指标来源；不显示为 0 或健康' },
  storage: { quality: 'not_connected', reason: '未接入存储使用率/容量/IO 时延指标来源；不显示为 0 或健康' },
  quality: 'not_connected',
  qualityNote: '该集群未接入中央运行时读取通道',
  generatedAt: '2026-09-13T06:40:00Z',
  source: 'core/v1 nodes + core/v1 pods (in-cluster)',
}

function renderPage(path = '/clusters/cluster-a/overview') {
  return render(
    <MemoryRouter initialEntries={[path]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <Routes><Route path="/clusters/:clusterUid/overview" element={<ClusterOverview />} /></Routes>
    </MemoryRouter>,
  )
}

describe('cluster page (V1.4 §6.3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getClusterOverview).mockResolvedValue({
      clusterId: 'cluster-a',
      name: '上海集群',
      environment: 'production',
      region: 'cn-east-1',
      status: 'degraded',
      statusReason: '采集覆盖不足',
      statusReasons: ['采集覆盖不足'],
      registrationStatus: 'ready',
      stale: false,
      version: 'v1.31.0',
      lastSyncAt: '2026-09-10T10:32:00Z',
      coverage: { covered: 118, expected: 120, ratio: 118 / 120 },
      issues: [{ clusterId: 'cluster-a', resourceUid: 'Pod/payments-1', ruleId: 'restart-high', severity: 'critical', status: 'firing', title: '容器重启次数异常', observedAt: '2026-09-13T06:30:00Z' }],
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
    vi.mocked(getPlatformCapacity).mockResolvedValue({
      clusters: [{
        clusterId: 'cluster-a',
        name: '上海集群',
        cpu: { used: 7.9, allocatable: 16, usageRatio: 7.9 / 16, unit: 'cores', aggregation: 'sum(node usage) / sum(node allocatable)', source: 'metrics.k8s.io/v1beta1 + core/v1 nodes.allocatable', sourceTimestamp: '2026-09-13T06:40:00Z' },
        memory: { used: 8 * 1024 ** 3, allocatable: 32 * 1024 ** 3, usageRatio: 0.25, unit: 'bytes', aggregation: 'sum(node usage) / sum(node allocatable)', source: 'metrics.k8s.io/v1beta1 + core/v1 nodes.allocatable', sourceTimestamp: '2026-09-13T06:40:00Z' },
        nodes: { total: 3, ready: 2, notReady: 1, unknown: 0 },
        p95CpuUtilization: 92.5,
        p95MemUtilization: 61.2,
        maxCpuUtilization: 97.5,
        maxMemUtilization: 70,
        hotNodeCount: 1,
        hotNodeThresholdPct: 80,
        quality: 'healthy',
        registrationStatus: 'active',
      }],
      count: 1,
      meta: { generatedAt: '2026-09-13T06:40:00Z', partial: false, stale: false, warningCodes: [] },
    })
    vi.mocked(getClusterRuntime).mockResolvedValue({
      ...notConnectedRuntime,
      pods: { total: 5, running: 4, pending: 1, failed: 0, succeeded: 0, unknown: 0, ready: 4, notReady: 1 },
      restarts: { totalPodsWithRestarts: 1, totalRestarts: 7, maxPerPod: 7, top: [{ namespace: 'observability', name: 'payments-1', restarts: 7 }], source: 'core/v1 pods.status.containerStatuses[].restartCount' },
      quality: 'partial',
      qualityNote: '存在 Pending/Failed/未就绪 Pod，集群整体状态不能按健康呈现',
    })
  })

  it('永久显示集群身份：环境/地域/Kubernetes 版本/时间窗/数据截止', async () => {
    renderPage()
    const identity = await screen.findByTestId('cluster-identity')
    expect(identity).toHaveTextContent('环境 production')
    expect(identity).toHaveTextContent('地域 cn-east-1')
    expect(identity).toHaveTextContent('Kubernetes v1.31.0')
    expect(identity).toHaveTextContent('时间窗')
    expect(identity).toHaveTextContent('数据截止')
  })

  it('shows cluster-scope CPU/memory with unit, aggregation and hot-node evidence', async () => {
    renderPage()
    expect(await screen.findByText('7.90 核 / 16.00 核')).toBeVisible()
    expect(screen.getByText('8.00 GiB / 32.00 GiB')).toBeVisible()
    expect(screen.getByText('P95 节点 CPU 利用率')).toBeVisible()
    expect(screen.getByText('92.5%')).toBeVisible()
    expect(screen.getByText('97.5%')).toBeVisible()
    expect(screen.getByText('热点节点数（≥80%）')).toBeVisible()
    expect(screen.getByText('2/3（未就绪 1）')).toBeVisible()
    // 口径与来源必须可对账
    expect(screen.getAllByText(/sum\(node usage\) \/ sum\(node allocatable\)/).length).toBeGreaterThan(0)
  })

  it('shows Pod scheduling and restart facts from real sources', async () => {
    renderPage()
    expect(await screen.findByText('Pod 调度与就绪')).toBeVisible()
    expect(screen.getByText('Pending')).toBeVisible()
    expect(screen.getByText('payments-1')).toBeVisible()
    expect(screen.getByText('7')).toBeVisible()
  })

  it('expresses network and storage as not connected instead of 0 or healthy', async () => {
    renderPage()
    expect(await screen.findByText('网络吞吐 / 错误 / 丢包 / 时延')).toBeVisible()
    expect(screen.getByText('存储使用率 / 容量 / IO 时延')).toBeVisible()
    expect(screen.getAllByText('未接入').length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByText(/未接入网络吞吐/).length).toBeGreaterThan(0)
    // 不得把未接入渲染成 0%
    expect(screen.queryByText('0.0%')).not.toBeInTheDocument()
  })

  it('links active alerts into AI 智能运维 with full context and never opens a standalone problem page', async () => {
    renderPage()
    const alertPanel = (await screen.findByText('活动告警')).closest('.card') as HTMLElement
    expect(within(alertPanel).getByText('容器重启次数异常')).toBeVisible()
    expect(within(alertPanel).getByText('Pod/payments-1')).toBeVisible()
    // 页面不得再链接到已废弃的独立调查/问题路由
    expect(document.body.innerHTML).not.toContain('/investigations')
    expect(document.body.innerHTML).not.toContain('/problems')
  })

  it('does not use the deprecated app golden-signal / service-chain / workload-health modules', async () => {
    renderPage()
    await screen.findByText('活动告警')
    for (const forbidden of ['应用黄金指标', '关键服务链', '工作负载健康分布']) {
      expect(screen.queryByText(forbidden)).not.toBeInTheDocument()
    }
    // KubeVirt 与 Kubernetes 资源作为上下文保留
    expect(screen.getByText('KubeVirt 虚拟机')).toBeVisible()
    expect(screen.getByText('节点与物理机')).toBeVisible()
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
    vi.mocked(getPlatformCapacity).mockResolvedValue({ clusters: [], count: 0, meta: { generatedAt: '2026-09-13T06:40:00Z', partial: true, stale: false, warningCodes: [] } })
    vi.mocked(getClusterRuntime).mockResolvedValue(notConnectedRuntime)

    renderPage('/clusters/stale-cluster/overview')

    expect(await screen.findByText('观测数据陈旧')).toBeVisible()
    expect(screen.getByText(/接入：已就绪/)).toBeVisible()
    const statusline = document.querySelector('.cluster-overview-statusline') as HTMLElement
    expect(within(statusline).getByText('未知')).toBeVisible()
    expect(within(statusline).queryByText('健康')).not.toBeInTheDocument()
    // 容量与运行时不可用时必须是显式不可用，不得显示 0 或健康
    expect(screen.getByText('容量事实不可用')).toBeVisible()
    expect(screen.getByText('Pod 来源未接入')).toBeVisible()
    expect(screen.getByText('重启事实不可用')).toBeVisible()
    expect(screen.queryByText('数据完整')).not.toBeInTheDocument()
  })
})
