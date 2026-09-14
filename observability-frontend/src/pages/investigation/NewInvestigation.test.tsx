import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import NewInvestigation from './NewInvestigation'
import { createRun } from '../../api/client'

vi.mock('../../api/client', () => ({ createRun: vi.fn() }))
vi.mock('../../features/scope/ScopeBar', () => ({ default: () => <div data-testid="scope-bar" /> }))
vi.mock('../../features/scope/ResourcePicker', () => ({ default: ({ value }: { value?: { uid: string } }) => <div data-testid="resource-picker">{value?.uid || 'picker'}</div> }))

const resource = {
  clusterId: 'cluster-a', uid: 'deployment/payment', type: 'deployment' as const, domain: 'kubernetes' as const, name: 'payment', namespace: 'payment',
}
const state = {
  clusters: [{ cluster_id: 'cluster-a', tenant_id: 'tenant-a', name: '生产集群 A', status: 'ready' }],
  authScope: { tenantId: 'tenant-a', activeClusterId: 'cluster-a' },
  active: { tenantId: 'tenant-a', clusterId: 'cluster-a', resource, timeRange: { mode: 'relative' as const, minutes: 60 as const } },
}

vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (value: typeof state) => unknown) => selector(state),
}))

describe('NewInvestigation', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-09-09T02:00:00.000Z'))
    vi.mocked(createRun).mockResolvedValue({ data: { run_id: 'run-1' } } as never)
  })

  afterEach(() => vi.useRealTimers())

  it('does not create a Run on page load and freezes a typed Kubernetes resource on submit', async () => {
    render(<MemoryRouter initialEntries={['/investigation/new?source=chat&clusterId=cluster-a&resource=deployment%2Fpayment&resourceType=deployment&resourceName=payment&namespace=payment']}><NewInvestigation /></MemoryRouter>)
    expect(createRun).not.toHaveBeenCalled()
    fireEvent.change(screen.getByPlaceholderText('例如：错误率突增且影响支付请求'), { target: { value: 'pods pending' } })
    fireEvent.click(screen.getByRole('button', { name: '发起调查' }))
    await waitFor(() => expect(createRun).toHaveBeenCalledTimes(1))
    expect(createRun).toHaveBeenCalledWith(expect.objectContaining({
      environment: 'prod', cluster_id: 'cluster-a', resource_id: 'deployment/payment', service: 'deployment/payment',
      target_type: 'deployment', namespace: 'payment',
      time_range_start: '2026-09-09T01:00:00.000Z', time_range_end: '2026-09-09T02:00:00.000Z',
    }))
  })

  it('submits a cluster-scope investigation without fabricating k8s_cluster or namespace', async () => {
    render(<MemoryRouter initialEntries={['/investigation/new']}><NewInvestigation /></MemoryRouter>)
    fireEvent.click(screen.getByLabelText('集群范围'))
    fireEvent.change(screen.getByPlaceholderText('例如：错误率突增且影响支付请求'), { target: { value: 'cluster health degraded' } })
    fireEvent.click(screen.getByRole('button', { name: '发起调查' }))
    await waitFor(() => expect(createRun).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(createRun).mock.calls[0][0]
    expect(payload).toMatchObject({ environment: 'prod', cluster_id: 'cluster-a', target_type: 'cluster' })
    expect(payload).not.toHaveProperty('resource_id')
    expect(payload).not.toHaveProperty('namespace')
  })
})
