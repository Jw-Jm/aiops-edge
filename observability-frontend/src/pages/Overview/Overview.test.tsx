import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Overview from './index'
import { getAlertEvents, getDashboardResources, getDashboardStats, getNodeMetrics } from '../../api/client'

vi.mock('../../api/client', () => ({
  getAlertEvents: vi.fn(), getDashboardResources: vi.fn(), getDashboardStats: vi.fn(), getNodeMetrics: vi.fn(),
}))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string; clusters: never[] }) => unknown) => selector({ currentClusterId: 'all', clusters: [] }),
}))

describe('Overview authentic failure states', () => {
  beforeEach(() => {
    vi.mocked(getDashboardStats).mockRejectedValue(new Error('dashboard unavailable'))
    vi.mocked(getDashboardResources).mockRejectedValue(new Error('resources unavailable'))
    vi.mocked(getNodeMetrics).mockRejectedValue(new Error('nodes unavailable'))
    vi.mocked(getAlertEvents).mockRejectedValue(new Error('alerts unavailable'))
  })

  it('shows an error state instead of claiming empty healthy data when alerts fail', async () => {
    render(<MemoryRouter><Overview /></MemoryRouter>)
    expect(await screen.findByText('alerts unavailable')).toBeInTheDocument()
    expect(screen.queryByText(/当前无活跃告警，系统健康/)).not.toBeInTheDocument()
  })
})
