import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  it('does not label a raw log with a missing severity as info', async () => {
    vi.mocked(queryLogs).mockResolvedValueOnce({
      data: {
        source: 'victorialogs',
        data: [{ timestamp: '2026-08-26 08:22:59', service_name: 'metrics-server', body: 'Post-timeout activity', severity: '' }],
      },
    } as never)

    render(<LogMetrics />)

    await waitFor(() => expect(screen.getByText('Post-timeout activity')).toBeInTheDocument())
    expect(screen.getByText('未知')).toBeInTheDocument()
    expect(screen.queryByText('info')).not.toBeInTheDocument()
  })

  it('does not let a slower initial query overwrite a newer level filter', async () => {
    let resolveInitial!: (value: unknown) => void
    let resolveFiltered!: (value: unknown) => void
    const initial = new Promise((resolve) => { resolveInitial = resolve })
    const filtered = new Promise((resolve) => { resolveFiltered = resolve })
    vi.mocked(queryLogs).mockReset()
    vi.mocked(queryLogs).mockImplementation((params: Record<string, unknown>) =>
      (params.level === 'error' ? filtered : initial) as never)

    const user = userEvent.setup()
    render(<LogMetrics />)
    await waitFor(() => expect(queryLogs).toHaveBeenCalledWith(expect.objectContaining({ source: 'victorialogs' })))

    await user.click(screen.getByText('全部级别'))
    await user.click(screen.getByText('错误'))
    await waitFor(() => expect(queryLogs).toHaveBeenCalledWith(expect.objectContaining({ level: 'error' })))

    resolveFiltered({ data: { source: 'victorialogs', data: [
      { timestamp: '2026-08-26 09:00:00', service_name: 'checkout', severity: 'ERROR', body: 'boom' },
      { timestamp: '2026-08-26 09:01:00', service_name: 'checkout', severity: '', body: 'unknown filtered row' },
    ] } })
    await waitFor(() => expect(screen.getByText('boom')).toBeInTheDocument())
    expect(screen.queryByText('unknown filtered row')).not.toBeInTheDocument()
    resolveInitial({ data: { source: 'victorialogs', data: [{ timestamp: '2026-08-26 08:00:00', service_name: 'checkout', severity: '', body: 'stale unfiltered row' }] } })
    await waitFor(() => expect(screen.queryByText('stale unfiltered row')).not.toBeInTheDocument())
  })
})
