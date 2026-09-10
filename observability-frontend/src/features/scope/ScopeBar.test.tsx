import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ScopeBar } from './ScopeBar'

const snapshot = {
  mode: 'snapshot' as const,
  runId: 'run-1',
  tenantId: 'tenant-a',
  clusterId: 'cluster-a',
  resource: { clusterId: 'cluster-a', uid: 'service:payment-api', type: 'service' as const, domain: 'application' as const, name: 'payment-api' },
  timeRange: { mode: 'absolute' as const, start: '2026-09-08T20:00:00Z', end: '2026-09-08T21:00:00Z' },
}

describe('ScopeBar', () => {
  it('renders a locked run snapshot without editable controls', () => {
    render(<MemoryRouter><ScopeBar snapshot={snapshot} /></MemoryRouter>)
    expect(screen.getByText('调查快照')).toBeInTheDocument()
    expect(screen.getByText(/cluster-a.*应用服务.*payment-api/)).toBeInTheDocument()
    expect(screen.queryByText('生产平台')).not.toBeInTheDocument()
    expect(screen.getByText(/20:00.*21:00/)).toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  })

  it('does not render a fake production platform layer in the editable scope bar', () => {
    render(<MemoryRouter><ScopeBar /></MemoryRouter>)
    expect(screen.queryByText('生产平台')).not.toBeInTheDocument()
  })
})
