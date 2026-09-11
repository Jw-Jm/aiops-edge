import { describe, expect, it } from 'vitest'
import { buildGraphDisplayModel, buildRelationChains, layoutForMode, relationLabel, resourceVisual, type GraphRelationRow } from './graphPresentation'
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

  it('builds relation chains only from real edges and never bridges disconnected components', () => {
    const rows = [
      row({ id: 'e1', sourceUid: 'a', targetUid: 'b', sourceName: 'orders', targetName: 'orders-pod', label: '包含', style: 'structural' }),
      // 与 a 不相连的独立连通分量：不得被串进同一个链。
      row({ id: 'e2', sourceUid: 'c', targetUid: 'd', sourceName: 'cache', targetName: 'cache-pod', label: '依赖', style: 'structural' }),
      // 传播故障的边，仅在 failure-chain 模式保留。
      row({ id: 'e3', sourceUid: 'b', targetUid: 'c', sourceName: 'orders-pod', targetName: 'cache', label: '依赖', style: 'failure', propagatesFailure: true }),
    ]

    const resourceChains = buildRelationChains(rows, 'a', 'resource-relations')
    expect(resourceChains.map((chain) => chain.id)).toEqual(['e1'])
    expect(resourceChains[0]).toMatchObject({ source: 'orders', relation: '包含', target: 'orders-pod', factStatus: 'fact' })

    const failureChains = buildRelationChains(rows, 'a', 'failure-chain')
    expect(failureChains.map((chain) => chain.id)).toEqual(['e3'])
    // 不得把不相邻节点按 vertices 顺序串成虚构链。
    expect(failureChains.every((chain) => rows.some((candidate) => candidate.id === chain.id))).toBe(true)
  })

  it('renders inferred relations as explicit inferred chains', () => {
    const rows = [row({ id: 'e9', sourceUid: 'a', targetUid: 'b', sourceName: 'orders', targetName: 'orders-pod', label: '疑似依赖', style: 'inferred', factStatus: 'inferred' })]
    const chains = buildRelationChains(rows, 'a', 'resource-relations')
    expect(chains).toHaveLength(1)
    expect(chains[0].factStatus).toBe('inferred')
  })
})

function row(overrides: Partial<GraphRelationRow> & Pick<GraphRelationRow, 'id' | 'sourceUid' | 'targetUid' | 'sourceName' | 'targetName'>): GraphRelationRow {
  return {
    source: overrides.sourceUid,
    target: overrides.targetUid,
    relationType: 'CONTAINS',
    label: '包含',
    style: 'structural',
    propagatesFailure: false,
    factStatus: 'fact',
    sourceRef: 'spec',
    syncedAt: '2026-09-10T10:32:00Z',
    ...overrides,
  } as GraphRelationRow
}
