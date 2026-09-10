import { describe, expect, it } from 'vitest'
import { buildGraphDisplayModel, layoutForMode, relationLabel, resourceVisual } from './graphPresentation'
import type { GraphSubgraph } from '../../api/graphContracts'

function graph(vertexCount = 4): GraphSubgraph {
  const vertices = Array.from({ length: vertexCount }, (_, index) => ({
    entity_uid: index === 0 ? 'service:center' : `${index % 2 ? 'vm' : 'pod'}:${index}`,
    entity_type: index === 0 ? 'service' : index % 2 ? 'vm' : 'pod', tenant_id: 'tenant-a', cluster_id: 'cluster-a', name: `node-${index}`, name_key: `node-${index}`, source: 'test', status: 'active', health: index === 1 ? 'critical' : 'healthy', confidence: 1, generation: 1, attrs_version: 1,
  }))
  return { center_entity_uid: 'service:center', vertices, edges: vertices.slice(1).map((vertex, index) => ({ edge_uid: `edge:${index}`, source_uid: 'service:center', target_uid: vertex.entity_uid, relation_type: index === 0 ? 'CONTAINS' : 'DEPENDS_ON', tenant_id: 'tenant-a', cluster_id: 'cluster-a', status: 'active', source: 'test', confidence: 1, generation: 1, propagates_failure: index === 0, candidate_direction: 'forward', impact_direction: 'downstream', attrs_version: 1 })), meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: false, stale: false, generated_at: '', warning_codes: [] } }
}

describe('graph presentation model', () => {
  it('maps graph modes to deterministic layouts and relation labels', () => {
    expect(layoutForMode('resource-relations')).toBe('dag-lr')
    expect(layoutForMode('failure-chain')).toBe('hierarchy-tb')
    expect(layoutForMode('expert')).toBe('dag-lr')
    expect(relationLabel('CONTAINS')).toBe('包含')
    expect(relationLabel('DEPENDS_ON')).toBe('依赖')
    expect(relationLabel('SOURCED_FROM')).toBe('来源于')
    expect(relationLabel('CONNECTS_TO_NAD')).toBe('连接到 NAD')
  })

  it('keeps the center, aggregates nodes beyond the visual budget and preserves typed icons', () => {
    const model = buildGraphDisplayModel(graph(12), { mode: 'expert', centerUid: 'service:center', maxNodes: 5, maxEdges: 3 })
    expect(model.nodes.length).toBeLessThanOrEqual(5)
    expect(model.nodes.some((node) => node.id === 'service:center')).toBe(true)
    expect(Object.values(model.omittedByType).reduce((sum, count) => sum + count, 0)).toBeGreaterThan(0)
    expect(model.nodes.some((node) => node.aggregate && node.label.includes('还有'))).toBe(true)
    expect(model.edges.length).toBeLessThanOrEqual(3)
    expect(resourceVisual('physical_server').iconKey).not.toBe(resourceVisual('k8s_node').iconKey)
    expect(resourceVisual('k8s_node').typeLabel).not.toBe(resourceVisual('vm').typeLabel)
  })

  it('uses severity styling independently from resource domain colors and exposes equivalent relation rows', () => {
    const model = buildGraphDisplayModel(graph(), { mode: 'failure-chain', centerUid: 'service:center', maxNodes: 80, maxEdges: 200 })
    expect(model.nodes.find((node) => node.id.includes('vm'))?.healthTone).toBe('critical')
    expect(model.edges[0].style).toBe('failure')
    expect(model.relationRows).toHaveLength(model.edges.length)
    expect(model.relationRows[0]).toMatchObject({ label: '包含', sourceName: 'node-0' })
  })
})
