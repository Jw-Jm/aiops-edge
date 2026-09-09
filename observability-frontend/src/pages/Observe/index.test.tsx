import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Observe from './index'
import { getAlertAggregation } from '../../api/client'
import { toProblem } from './index'

vi.mock('../../api/client', () => ({ getAlertAggregation: vi.fn() }))
vi.mock('../alerts/AlertEvents', () => ({ default: () => <div>原始告警</div> }))
vi.mock('../observability/Trace', () => ({ default: () => <div>调用链</div> }))
vi.mock('../observability/LogMetrics', () => ({ default: () => <div>日志与指标</div> }))
vi.mock('../infra/Changes', () => ({ default: () => <div>变更时间线</div> }))
vi.mock('../observability/Grafana', () => ({ default: () => <div>Grafana</div> }))
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: { authScope: { activeClusterId: string } | null }) => unknown) => selector({ authScope: { activeClusterId: 'cluster-1' } }),
}))

describe('Observe unified entry', () => {
  beforeEach(() => vi.mocked(getAlertAggregation).mockResolvedValue({ data: { data: [] } } as never))

  it('loads server aggregated problems as the default view', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><MemoryRouter initialEntries={['/observe']}><Observe /></MemoryRouter></QueryClientProvider>)
    expect(screen.getByRole('tab', { name: '问题' })).toBeInTheDocument()
    await waitFor(() => expect(getAlertAggregation).toHaveBeenCalled())
    expect(screen.getByText('暂无问题')).toBeInTheDocument()
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
})
