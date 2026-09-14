import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ResourcePicker } from './ResourcePicker'
import type { ResourceCatalogItem } from '../../api/resources'

const getResourceCatalog = vi.fn()
const getResourceDetail = vi.fn()

vi.mock('../../api/resources', async () => {
  const actual = await vi.importActual<typeof import('../../api/resources')>('../../api/resources')
  return { ...actual, getResourceCatalog: (...args: unknown[]) => getResourceCatalog(...args), getResourceDetail: (...args: unknown[]) => getResourceDetail(...args) }
})

function item(overrides: Partial<ResourceCatalogItem> = {}): ResourceCatalogItem {
  return {
    clusterId: 'cluster-a', uid: 'pod:payments/api-01', type: 'pod', domain: 'kubernetes', name: 'api-01', namespace: 'payments',
    location: 'payments / api-01', health: 'healthy', source: 'graph', ...overrides,
  }
}

afterEach(() => {
  getResourceCatalog.mockReset()
  getResourceDetail.mockReset()
  window.history.replaceState({}, '', '/')
})

describe('ResourcePicker', () => {
  it('does not request for a one-character query, then debounces and groups five-domain results', async () => {
    getResourceCatalog.mockResolvedValue({ items: [
      item({ uid: 'server:01', type: 'physical_server', domain: 'compute', name: 'edge-01', namespace: undefined, location: '机房 A / edge-01', health: 'degraded' }),
      item({ uid: 'service:01', type: 'service', domain: 'application', name: 'checkout', namespace: undefined, location: 'checkout', health: 'critical' }),
    ], meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
    render(<MemoryRouter><ResourcePicker clusterId="cluster-a" onChange={vi.fn()} /></MemoryRouter>)
    fireEvent.click(screen.getByRole('combobox', { name: '资源' }))
    fireEvent.change(screen.getByRole('textbox', { name: '搜索资源' }), { target: { value: 'a' } })
    await new Promise((resolve) => setTimeout(resolve, 300))
    expect(getResourceCatalog).not.toHaveBeenCalled()

    fireEvent.change(screen.getByRole('textbox', { name: '搜索资源' }), { target: { value: 'api' } })
    expect(getResourceCatalog).not.toHaveBeenCalled()
    await waitFor(() => expect(getResourceCatalog).toHaveBeenCalledWith({ q: 'api', limit: 40, health: undefined }, expect.any(AbortSignal)))
    expect(screen.getByText('计算')).toBeInTheDocument()
    expect(screen.getByText('应用')).toBeInTheDocument()
    expect(screen.getByText('物理服务器')).toBeInTheDocument()
    expect(screen.getByText('机房 A / edge-01')).toBeInTheDocument()
    expect(screen.getByText('应用服务')).toBeInTheDocument()
    expect(screen.getByText('checkout')).toBeInTheDocument()
  })

  it('restores a URL resource only when it belongs to the active cluster', async () => {
    const onChange = vi.fn()
    getResourceDetail.mockResolvedValue({ data: item({ uid: 'pod:payments/api-02', name: 'api-02' }), meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
    render(<MemoryRouter initialEntries={['/?resource=pod%3Apayments%2Fapi-02']}><ResourcePicker clusterId="cluster-a" onChange={onChange} /></MemoryRouter>)
    await waitFor(() => expect(getResourceDetail).toHaveBeenCalledWith('pod:payments/api-02', expect.any(AbortSignal)))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ uid: 'pod:payments/api-02' }))
  })

  it('clears a cross-cluster URL resource and reports the authorization boundary', async () => {
    getResourceDetail.mockResolvedValue({ data: item({ clusterId: 'cluster-b', uid: 'pod:other/api-02' }), meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
    render(<MemoryRouter initialEntries={['/?resource=pod%3Aother%2Fapi-02']}><ResourcePicker clusterId="cluster-a" onChange={vi.fn()} /></MemoryRouter>)
    expect(await screen.findByRole('alert')).toHaveTextContent('资源不属于当前集群，已清除资源范围')
    expect(screen.queryByText('pod:other/api-02')).not.toBeInTheDocument()
  })
})
