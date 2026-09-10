import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import InvestigationCenter from './InvestigationCenter'
import { listRuns } from '../../api/client'

vi.mock('../../api/client', () => ({ listRuns: vi.fn() }))
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: { authScope: { activeClusterId: string } | null }) => unknown) => selector({ authScope: { activeClusterId: 'cluster-1' } }),
}))

describe('InvestigationCenter identity projection', () => {
  beforeEach(() => {
    vi.mocked(listRuns).mockResolvedValue({ data: { runs: [{
      run_id: 'run-1', request_id: 'request-1', tenant_id: 'tenant-1',
      primary_cluster_id: 'cluster-1', target_resource_id: 'checkout', intent: 'investigate',
      target_type: 'service',
      status: 'created', principal_id: 'user-123', created_by: 'user-123', created_at: '2026-08-26T00:00:00Z',
    }, {
      run_id: 'run-other', request_id: 'request-other', tenant_id: 'tenant-1',
      primary_cluster_id: 'cluster-2', target_resource_id: 'other', intent: 'other',
      target_type: 'pod', status: 'created', principal_id: 'user-456', created_by: 'user-456', created_at: '2026-08-26T00:00:00Z',
    }] } } as never)
  })

  it('renders the persisted run principal instead of a fixed system identity', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><InvestigationCenter /></MemoryRouter>)
    expect(await screen.findByText('user-123')).toBeInTheDocument()
    expect(screen.queryByText('user-456')).not.toBeInTheDocument()
    expect(screen.getByText('应用服务')).toBeInTheDocument()
    expect(screen.queryByText('system')).not.toBeInTheDocument()
  })

  it('shows an error state when the persisted run source is unavailable', async () => {
    vi.mocked(listRuns).mockRejectedValueOnce(new Error('run store unavailable'))
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><InvestigationCenter /></MemoryRouter>)
    expect(await screen.findByText('run store unavailable')).toBeInTheDocument()
  })

  it('keeps the run list resource-first and exposes frozen scope and evidence count', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><InvestigationCenter /></MemoryRouter>)
    await screen.findByText('user-123')
    expect(await screen.findByText('冻结窗口')).toBeInTheDocument()
    expect(screen.getByText('证据')).toBeInTheDocument()
  })
})
