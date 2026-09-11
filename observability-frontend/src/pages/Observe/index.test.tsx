import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Observe from './index'
import source from './index.tsx?raw'
import { getAlertAggregation } from '../../api/client'
import { toProblem } from './index'

vi.mock('../../api/client', () => ({ getAlertAggregation: vi.fn() }))
vi.mock('../alerts/AlertEvents', () => ({ default: () => <div>原始告警</div> }))
vi.mock('../observability/Trace', () => ({ default: () => <div>调用链</div> }))
vi.mock('../observability/LogMetrics', () => ({ default: () => <div>日志与指标</div> }))
vi.mock('../infra/Changes', () => ({ default: () => <div>变更时间线</div> }))
vi.mock('../observability/Grafana', () => ({ default: () => <div>Grafana</div> }))
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: {
    authScope: { activeClusterId: string } | null
    clusters?: Array<{ cluster_id: string; name: string }>
  }) => unknown) => selector({
    authScope: { activeClusterId: 'cluster-1' },
    clusters: [{ cluster_id: 'cloud-sh-01', name: '上海一号' }],
  }),
}))

describe('Observe unified entry', () => {
  beforeEach(() => vi.mocked(getAlertAggregation).mockResolvedValue({ data: { data: [] } } as never))

  it('loads server aggregated problems as the default view', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/observe']}><Observe /></MemoryRouter></QueryClientProvider>)
    expect(screen.getByRole('tab', { name: '问题' })).toBeInTheDocument()
    await waitFor(() => expect(getAlertAggregation).toHaveBeenCalled())
    // 空态现在与 loading/error 互斥渲染，需等待查询完成后断言。
    expect(await screen.findByText('暂无问题')).toBeInTheDocument()
  })

  it('projects a cluster-scoped problem without inventing a service resource', () => {
    const problem = toProblem({ service: '', total: 2, by_severity: { critical: 1 }, latest_rule: 'cluster pressure', latest_time: '2026-09-09T01:00:00Z', events: [] }, 'cluster-1')
    expect(problem.resource).toBeUndefined()
    expect(problem.cluster_id).toBe('cluster-1')
    expect(problem.affected_resources).toEqual([])
  })

  it('falls back unknown views to the resource-first problems queue', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/observe?view=not-a-view']}><Observe /></MemoryRouter></QueryClientProvider>)
    expect(screen.getByRole('tab', { name: '问题' })).toHaveAttribute('aria-selected', 'true')
  })

  it('does not present a healthy empty state when aggregation fails', async () => {
    vi.mocked(getAlertAggregation).mockRejectedValueOnce(new Error('permission_denied'))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/observe']}><Observe /></MemoryRouter></QueryClientProvider>)
    expect(await screen.findByText('问题数据读取失败')).toBeVisible()
    expect(screen.queryByText('暂无问题')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('问题事实摘要')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeVisible()
  })

  it('keeps loading, empty and ready problem states mutually exclusive', async () => {
    vi.mocked(getAlertAggregation).mockResolvedValue({ data: { data: [] } } as never)
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/observe']}><Observe /></MemoryRouter></QueryClientProvider>)
    expect(await screen.findByText('暂无问题')).toBeVisible()
    expect(screen.queryByLabelText('问题事实摘要')).not.toBeInTheDocument()
    expect(screen.queryByText('问题数据读取失败')).not.toBeInTheDocument()
  })

  it('shows the real cluster and resource health facts before raw signals', async () => {
    vi.mocked(getAlertAggregation).mockResolvedValueOnce({ data: { data: [{
      service: 'order-api', total: 3, by_severity: { critical: 1 }, latest_rule: 'Pod 未就绪', latest_time: '2026-09-10T01:00:00Z',
      cluster_id: 'cloud-sh-01', resource_uid: 'deployment/order-api', resource_type: 'deployment', resource_name: 'order-api',
      failure_rate: 0.24, ready_replicas: 2, desired_replicas: 3, events: [],
    }] } } as never)
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/observe']}><Observe /></MemoryRouter></QueryClientProvider>)
    // 集群列显示集群名（<xl 视口按设计收起），完整 cluster_id 可复制、不逐字换行。
    expect(source).toContain("title: '集群'")
    expect(source).toContain('copyable={{ text: clusterId }}')
    expect(await screen.findByText('失败率')).toBeVisible()
    expect(screen.getByText('就绪副本')).toBeVisible()
    expect(screen.getByRole('tab', { name: '指标' })).toBeVisible()
    expect(screen.getByRole('tab', { name: '日志' })).toBeVisible()
    expect(screen.getByRole('tab', { name: 'Trace' })).toBeVisible()
    expect(screen.getByRole('tab', { name: '事件' })).toBeVisible()
    expect(screen.queryByText('全部环境')).not.toBeInTheDocument()
  })
})

describe('alert investigation linking', () => {
  const renderObserve = () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(<QueryClientProvider client={queryClient}><MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><Observe /></MemoryRouter></QueryClientProvider>)
  }

  it('renders the governed investigation status and a read-only notice', async () => {
    vi.mocked(getAlertAggregation).mockResolvedValue({ data: { data: [{
      service: 'order-api', total: 1, by_severity: { critical: 1 }, latest_rule: 'Pod 未就绪',
      latest_time: '2026-09-11T08:00:00Z', cluster_id: 'cluster-1', events: [{ id: 'event-1' }],
    }] } } as never)
    renderObserve()
    expect(await screen.findByText('未创建调查')).toBeVisible()
    expect(screen.getByRole('button', { name: '发起调查' })).toBeVisible()
    expect(screen.getByText('自动调查仅只读，不会执行处置')).toBeVisible()
  })

  it('labels a skipped alert with a readable reason instead of hiding it', async () => {
    vi.mocked(getAlertAggregation).mockResolvedValue({ data: { data: [{
      service: 'order-api', total: 1, by_severity: { warning: 1 }, latest_rule: 'CPU 高',
      latest_time: '2026-09-11T08:00:00Z', cluster_id: 'cluster-1',
      events: [{ id: 'event-2', investigation_link: { mode: 'auto_readonly', status: 'skipped', reason_code: 'below_severity' } }],
    }] } } as never)
    renderObserve()
    expect(await screen.findByText('已跳过')).toBeVisible()
    expect(screen.getByText('严重度不足')).toBeVisible()
  })
})
