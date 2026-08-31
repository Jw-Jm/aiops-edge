import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import AlertRules from './AlertRules'
import { getAlertRules } from '../../api/client'

vi.mock('../../api/client', () => ({
  getAlertRules: vi.fn(),
  createAlertRule: vi.fn(),
  updateAlertRule: vi.fn(),
  deleteAlertRule: vi.fn(),
}))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string }) => unknown) => selector({ currentClusterId: 'all' }),
}))

describe('AlertRules service projection', () => {
  beforeEach(() => {
    vi.mocked(getAlertRules).mockResolvedValue({
      data: [{ id: 'rule-1', name: 'canary rule', service: 'aiops-mutation-canary', metric: 'error_rate', threshold: 9999, severity: 'warning', enabled: true }],
    } as never)
  })

  it('renders the persisted service field returned by the alert-rule API', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><AlertRules /></MemoryRouter>)

    await waitFor(() => expect(screen.getByText('aiops-mutation-canary')).toBeInTheDocument())
    expect(screen.queryByText('所有服务')).not.toBeInTheDocument()
  })
})
