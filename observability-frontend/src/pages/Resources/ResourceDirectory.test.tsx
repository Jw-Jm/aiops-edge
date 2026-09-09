import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import ResourceDirectory from './ResourceDirectory'

const getResourceCatalog = vi.fn()
vi.mock('../../api/resources', async () => {
  const actual = await vi.importActual<typeof import('../../api/resources')>('../../api/resources')
  return { ...actual, getResourceCatalog: (...args: unknown[]) => getResourceCatalog(...args) }
})

afterEach(() => getResourceCatalog.mockReset())

describe('ResourceDirectory', () => {
  it('reads filters from URL, renders typed locations and selects by UID', async () => {
    getResourceCatalog.mockResolvedValue({ items: [{ clusterId: 'cluster-a', uid: 'pod:payments/api-01', type: 'pod', domain: 'kubernetes', name: 'api-01', namespace: 'payments', location: 'payments / api-01', health: 'critical', source: 'graph' }], meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
    const onSelect = vi.fn()
    render(<MemoryRouter initialEntries={['/resources?domain=kubernetes&q=api']}><ResourceDirectory clusterId="cluster-a" onSelect={onSelect} /></MemoryRouter>)
    await waitFor(() => expect(getResourceCatalog).toHaveBeenCalledWith(expect.objectContaining({ domain: 'kubernetes', q: 'api', limit: 50 }), expect.any(AbortSignal)))
    const row = screen.getByRole('button', { name: /Pod payments \/ api-01/ })
    expect(row).toHaveTextContent('payments / api-01')
    fireEvent.click(row)
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ uid: 'pod:payments/api-01' }))
  })
})
