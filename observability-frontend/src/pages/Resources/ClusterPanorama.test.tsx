import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import ClusterPanorama from './ClusterPanorama'

const getResourceSummary = vi.fn()
vi.mock('../../api/resources', async () => {
  const actual = await vi.importActual<typeof import('../../api/resources')>('../../api/resources')
  return { ...actual, getResourceSummary: (...args: unknown[]) => getResourceSummary(...args) }
})

afterEach(() => getResourceSummary.mockReset())

describe('ClusterPanorama', () => {
  it('shows cluster identity, completeness, five domain health cards and no full graph by default', async () => {
    getResourceSummary.mockResolvedValue({ domains: [
      { domain: 'compute', count: 8, health: { healthy: 7, degraded: 1 }, incomplete: false },
      { domain: 'network', count: 2, health: { healthy: 2 }, incomplete: false },
    ], meta: { generatedAt: '', partial: false, stale: false, warningCodes: [] } })
    render(<ClusterPanorama clusterId="cluster-a" clusterName="生产集群 A" />)
    await waitFor(() => expect(screen.getByText('集群身份')).toBeInTheDocument())
    expect(screen.getByText('生产集群 A')).toBeInTheDocument()
    expect(screen.getByText('采集完整度')).toBeInTheDocument()
    expect(screen.getByText('计算')).toBeInTheDocument()
    expect(screen.getByText('存储')).toBeInTheDocument()
    expect(screen.getByText('Kubernetes')).toBeInTheDocument()
    expect(screen.getByText('应用服务')).toBeInTheDocument()
    expect(screen.getByText('异常队列')).toBeInTheDocument()
    expect(screen.getByText('容量风险')).toBeInTheDocument()
    expect(screen.queryByText('关系探索')).not.toBeInTheDocument()
  })
})
