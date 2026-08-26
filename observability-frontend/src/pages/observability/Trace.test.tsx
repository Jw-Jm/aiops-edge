import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Trace from './Trace'
import { getServices, getTraces } from '../../api/client'

vi.mock('../../api/client', () => ({ getServices: vi.fn(), getTraces: vi.fn(), getTraceDetail: vi.fn(), getTraceContext: vi.fn() }))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string }) => unknown) => selector({ currentClusterId: 'all' }),
}))

describe('Trace authentic failure states', () => {
  beforeEach(() => {
    vi.mocked(getTraces).mockRejectedValue(new Error('trace backend unavailable'))
    vi.mocked(getServices).mockResolvedValue({ data: [] } as never)
  })

  it('shows an error state instead of an empty trace table when the query fails', async () => {
    render(<MemoryRouter><Trace /></MemoryRouter>)
    expect(await screen.findByText('trace backend unavailable')).toBeInTheDocument()
  })
})
