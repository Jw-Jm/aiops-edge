import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import CallMatrix from './CallMatrix'

describe('CallMatrix', () => {
  it('renders a row/column call matrix with metrics, not an edge dump', () => {
    render(<CallMatrix
      vertices={[
        { entity_uid: 'a', entity_type: 'service', tenant_id: 't', cluster_id: 'c', name: 'checkout', name_key: 'checkout', source: 'trace', status: 'active', confidence: 1, generation: 1, attrs_version: 1 },
        { entity_uid: 'b', entity_type: 'service', tenant_id: 't', cluster_id: 'c', name: 'payments', name_key: 'payments', source: 'trace', status: 'active', confidence: 1, generation: 1, attrs_version: 1 },
      ]}
      edges={[{ edge_uid: 'e', source_uid: 'a', target_uid: 'b', relation_type: 'DEPENDS_ON', tenant_id: 't', cluster_id: 'c', status: 'active', source: 'trace', confidence: 1, generation: 1, attrs_version: 1, propagates_failure: true, candidate_direction: 'OUT', impact_direction: 'OUT', attrs: { calls: 12, error_rate: 0.25, latency_ms: 38 } }]}
    />)
    expect(screen.getByRole('columnheader', { name: '调用方 / 被调用方' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'checkout' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'payments' })).toBeInTheDocument()
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(screen.getByText('25.0%')).toBeInTheDocument()
    expect(screen.getByText('38ms')).toBeInTheDocument()
  })
})
