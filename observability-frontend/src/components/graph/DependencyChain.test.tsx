import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import DependencyChain from './DependencyChain'
import type { GraphEdge, GraphEntity } from '../../api/graphContracts'

function entity(uid: string, name: string, entityType = 'pod'): GraphEntity {
  return {
    entity_uid: uid, entity_type: entityType, tenant_id: 'tenant-a', cluster_id: 'cluster-a',
    name, name_key: name, source: 'test', status: 'active', confidence: 1, generation: 1, attrs_version: 1,
  }
}

function edge(uid: string, source: string, target: string, relationType: string, propagatesFailure = false): GraphEdge {
  return {
    edge_uid: uid, source_uid: source, target_uid: target, relation_type: relationType,
    tenant_id: 'tenant-a', cluster_id: 'cluster-a', status: 'active', source: 'test',
    confidence: 1, generation: 1, propagates_failure: propagatesFailure,
    candidate_direction: 'forward', impact_direction: 'downstream', attrs_version: 1,
  }
}

describe('DependencyChain', () => {
  it('renders one real relation per row instead of chaining vertices by order', () => {
    render(
      <DependencyChain
        vertices={[entity('uid-a', 'orders', 'deployment'), entity('uid-b', 'orders-pod', 'pod'), entity('uid-c', 'cache', 'statefulset')]}
        edges={[edge('e1', 'uid-a', 'uid-b', 'CONTAINS'), edge('e2', 'uid-b', 'uid-c', 'DEPENDS_ON')]}
        centerUid="uid-b"
        mode="resource-relations"
      />,
    )

    expect(screen.getByRole('heading', { name: '关键关系链' })).toBeVisible()
    const rows = screen.getAllByTestId('relation-chain-row')
    expect(rows).toHaveLength(2)
    // 每行必须同时包含源、中文关系和目标。
    expect(rows[0]).toHaveTextContent('orders')
    expect(rows[0]).toHaveTextContent('包含')
    expect(rows[0]).toHaveTextContent('orders-pod')
    expect(rows[1]).toHaveTextContent('依赖')
    expect(rows[1]).toHaveTextContent('cache')
  })

  it('shows an explicit empty state when no real edge qualifies', () => {
    render(
      <DependencyChain
        vertices={[entity('uid-a', 'orders', 'deployment'), entity('uid-orphan', 'orphan', 'pod')]}
        edges={[]}
        centerUid="uid-a"
        mode="resource-relations"
      />,
    )
    expect(screen.getByText('尚未形成可验证关系链')).toBeVisible()
    expect(screen.queryAllByTestId('relation-chain-row')).toHaveLength(0)
  })

  it('only keeps failure-propagating edges in failure-chain mode', () => {
    render(
      <DependencyChain
        vertices={[entity('uid-a', 'orders', 'deployment'), entity('uid-b', 'orders-pod', 'pod'), entity('uid-c', 'cache', 'statefulset')]}
        edges={[edge('e1', 'uid-a', 'uid-b', 'CONTAINS'), edge('e2', 'uid-b', 'uid-c', 'DEPENDS_ON', true)]}
        centerUid="uid-b"
        mode="failure-chain"
      />,
    )
    const rows = screen.getAllByTestId('relation-chain-row')
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveTextContent('cache')
  })
})
