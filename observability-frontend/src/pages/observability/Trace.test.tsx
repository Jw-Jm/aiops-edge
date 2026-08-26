import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Trace, { buildSpanTree } from './Trace'
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

describe('Trace span tree safety', () => {
  it('terminates and renders each span once when duplicate IDs create a parent cycle', () => {
    const { roots } = buildSpanTree([
      { span_id: 'root', parent_span_id: '', ms: 1, start_time: '2026-08-26 08:00:00' },
      // A duplicate ID with a different parent can make the map-backed tree
      // point back to root even though the first row classified root as a root.
      { span_id: 'root', parent_span_id: 'child', ms: 1, start_time: '2026-08-26 08:00:00' },
      { span_id: 'child', parent_span_id: 'root', ms: 2, start_time: '2026-08-26 08:00:00' },
    ])

    expect(roots.map(({ span }) => span.span_id)).toEqual(['root', 'child'])
  })
})
