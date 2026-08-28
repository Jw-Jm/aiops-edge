import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import ServiceObservability from './ServiceObservability'

vi.mock('../../components/graph/GraphMap', () => ({ default: () => <div data-testid="graph-map" /> }))
vi.mock('../../components/graph/GraphExplorer', () => ({ default: () => <section aria-label="专家关系探索" /> }))
vi.mock('../../api/client', () => ({
  getServices: vi.fn(),
  getServiceDetail: vi.fn(),
}))
vi.mock('../../api/knowledgeGraph', () => ({
  getGraphHealth: vi.fn(),
  getGraphImpact: vi.fn(),
  searchGraphEntities: vi.fn(),
  getGraphNeighbors: vi.fn(),
}))

import { getServices } from '../../api/client'
import { getGraphHealth, getGraphImpact, getGraphNeighbors, searchGraphEntities } from '../../api/knowledgeGraph'

const graph = {
  center_entity_uid: 'service:checkout',
  vertices: [
    { entity_uid: 'service:checkout', entity_type: 'service', tenant_id: 't', cluster_id: 'c', name: 'checkout', name_key: 'checkout', source: 'trace', status: 'active', confidence: 1, generation: 1, attrs_version: 1 },
    { entity_uid: 'service:payments', entity_type: 'service', tenant_id: 't', cluster_id: 'c', name: 'payments', name_key: 'payments', source: 'trace', status: 'active', confidence: 1, generation: 1, attrs_version: 1 },
  ],
  edges: [{ edge_uid: 'edge:checkout-payments', source_uid: 'service:checkout', target_uid: 'service:payments', relation_type: 'DEPENDS_ON', tenant_id: 't', cluster_id: 'c', status: 'active', source: 'trace', confidence: 1, generation: 1, attrs_version: 1, propagates_failure: true, candidate_direction: 'OUT', impact_direction: 'OUT' }],
  meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: false, stale: false, generated_at: '', warning_codes: [] },
}

describe('ServiceObservability service panorama', () => {
  beforeEach(() => {
    vi.mocked(getServices).mockResolvedValue({ data: [{ service_name: 'checkout', traces: 10, errors: 1, error_rate: 0.1, avg_ms: 42 }] } as never)
    vi.mocked(getGraphHealth).mockResolvedValue({ data: { ready: true, backend: 'hugegraph', schema_version: 2 } } as never)
    vi.mocked(searchGraphEntities).mockResolvedValue({ data: { items: [graph.vertices[0]], count: 1 } } as never)
    vi.mocked(getGraphNeighbors).mockResolvedValue({ data: graph } as never)
    vi.mocked(getGraphImpact).mockResolvedValue({ data: graph } as never)
  })

  it('renders the required panorama sections instead of a force/topology toggle', async () => {
    render(<MemoryRouter><ServiceObservability /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText('服务列表')).toBeInTheDocument())
    expect(screen.getByText('服务摘要')).toBeInTheDocument()
    expect(screen.getByText('服务地图')).toBeInTheDocument()
    expect(screen.getByText('依赖主链')).toBeInTheDocument()
    expect(screen.getByText('调用矩阵')).toBeInTheDocument()
    expect(screen.getByText('专家关系探索')).toBeInTheDocument()
    expect(screen.queryByText('拓扑视图')).not.toBeInTheDocument()
    expect(getGraphNeighbors).toHaveBeenCalledWith('service:checkout', expect.objectContaining({ depth: 2 }))
  })
})
