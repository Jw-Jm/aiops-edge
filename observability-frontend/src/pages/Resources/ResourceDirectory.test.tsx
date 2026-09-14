import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import ResourceDirectory from './ResourceDirectory'

const getResourceCatalog = vi.fn()
vi.mock('../../api/resources', async () => {
  const actual = await vi.importActual<typeof import('../../api/resources')>('../../api/resources')
  return { ...actual, getResourceCatalog: (...args: unknown[]) => getResourceCatalog(...args) }
})

afterEach(() => getResourceCatalog.mockReset())

describe('ResourceDirectory', () => {
  it('reads group filters from URL, renders typed locations and selects by UID', async () => {
    getResourceCatalog.mockResolvedValue({ items: [{ clusterId: 'cluster-a', uid: 'pod:payments/api-01', type: 'pod', domain: 'kubernetes', name: 'api-01', namespace: 'payments', location: 'payments / api-01', health: 'critical', source: 'graph' }], total: 1, meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
    const onSelect = vi.fn()
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/resources?group=containers&type=pod&namespace=payments&health=critical&freshness=fresh&q=api']}><ResourceDirectory clusterId="cluster-a" onSelect={onSelect} /></MemoryRouter>)
    await waitFor(() => expect(getResourceCatalog).toHaveBeenCalledWith(expect.objectContaining({ group: 'containers', type: 'pod', namespace: 'payments', health: 'critical', freshness: 'fresh', q: 'api', limit: 50 }), expect.any(AbortSignal)))
    expect(screen.getByText('容器资源')).toBeVisible()
    const row = screen.getByRole('button', { name: /Pod payments \/ api-01/ })
    expect(row).toHaveTextContent('payments / api-01')
    fireEvent.click(row)
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ uid: 'pod:payments/api-01' }))
  })

  it('debounces resource search before requesting the catalog', async () => {
    vi.useFakeTimers()
    try {
      getResourceCatalog.mockResolvedValue({ items: [], total: 0, meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
      render(<MemoryRouter initialEntries={['/clusters/cluster-a/resources?group=containers']}><ResourceDirectory clusterId="cluster-a" /></MemoryRouter>)
      await act(async () => { await Promise.resolve() })
      expect(getResourceCatalog).toHaveBeenCalledTimes(1)
      fireEvent.change(screen.getByRole('searchbox', { name: '资源目录搜索' }), { target: { value: 'api' } })
      expect(getResourceCatalog).toHaveBeenCalledTimes(1)
      await act(async () => { await vi.advanceTimersByTimeAsync(250); await Promise.resolve() })
      expect(getResourceCatalog).toHaveBeenCalledTimes(2)
      expect(getResourceCatalog).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'api' }), expect.any(AbortSignal))
    } finally {
      vi.useRealTimers()
    }
  })
})
