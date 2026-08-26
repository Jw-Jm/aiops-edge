import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LogMetrics from './LogMetrics'
import { aggregateLogs, queryLogs } from '../../api/client'

vi.mock('../../api/client', () => ({ queryLogs: vi.fn(), aggregateLogs: vi.fn() }))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string }) => unknown) => selector({ currentClusterId: 'all' }),
}))

describe('LogMetrics supported source projection', () => {
  beforeEach(() => {
    vi.mocked(queryLogs).mockResolvedValue({ data: { data: [], count: 0, source: 'victorialogs' } } as never)
    vi.mocked(aggregateLogs).mockResolvedValue({ data: { services: [] } } as never)
  })

  it('uses VictoriaLogs and does not expose the empty ClickHouse raw-log option', async () => {
    render(<LogMetrics />)

    await waitFor(() => expect(queryLogs).toHaveBeenCalledWith(expect.objectContaining({ source: 'victorialogs' })))
    expect(screen.getByText('数据源 · VictoriaLogs')).toBeInTheDocument()
    expect(screen.queryByText('数据源 · ClickHouse')).not.toBeInTheDocument()
  })
})
