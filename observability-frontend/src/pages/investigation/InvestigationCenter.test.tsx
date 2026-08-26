import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import InvestigationCenter from './InvestigationCenter'
import { listRuns } from '../../api/client'

vi.mock('../../api/client', () => ({ listRuns: vi.fn() }))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string }) => unknown) => selector({ currentClusterId: 'all' }),
}))

describe('InvestigationCenter identity projection', () => {
  beforeEach(() => {
    vi.mocked(listRuns).mockResolvedValue({ data: { runs: [{
      run_id: 'run-1', request_id: 'request-1', tenant_id: 'tenant-1',
      primary_cluster_id: 'cluster-1', target_resource_id: 'checkout', intent: 'investigate',
      status: 'created', principal_id: 'user-123', created_by: 'user-123', created_at: '2026-08-26T00:00:00Z',
    }] } } as never)
  })

  it('renders the persisted run principal instead of a fixed system identity', async () => {
    render(<MemoryRouter><InvestigationCenter /></MemoryRouter>)
    expect(await screen.findByText('user-123')).toBeInTheDocument()
    expect(screen.queryByText('system')).not.toBeInTheDocument()
  })

  it('shows an error state when the persisted run source is unavailable', async () => {
    vi.mocked(listRuns).mockRejectedValueOnce(new Error('run store unavailable'))
    render(<MemoryRouter><InvestigationCenter /></MemoryRouter>)
    expect(await screen.findByText('run store unavailable')).toBeInTheDocument()
  })
})
