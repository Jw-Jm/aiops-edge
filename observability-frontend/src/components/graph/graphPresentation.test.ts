import { describe, expect, it } from 'vitest'
import { buildGraphDisplayModel, buildRelationChains, focusGraph, isContextEntityType, layoutForMode, relationLabel, relationSemantic, resourceVisual, RELATION_SEMANTIC_LABELS, RELATION_SEMANTICS, type GraphRelationRow } from './graphPresentation'
import type { GraphEdge, GraphSubgraph } from '../../api/graphContracts'

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

  it('uses red only for failure propagation mode', () => {
    const input = graph()
    input.edges[0].relation_type = 'CONTAINS'
    input.edges[0].propagates_failure = true
    const options = { centerUid: input.center_entity_uid, maxNodes: 80, maxEdges: 200 }
    expect(buildGraphDisplayModel(input, { ...options, mode: 'resource-relations' }).edges[0].style).toBe('structural')
    expect(buildGraphDisplayModel(input, { ...options, mode: 'failure-chain' }).edges[0].style).toBe('failure')
    input.edges[0].status = 'inferred'
    expect(buildGraphDisplayModel(input, { ...options, mode: 'failure-chain' }).edges[0].style).toBe('inferred')
  })

  it('maps every raw relation type to a stable semantic and never guesses by colour', () => {
    expect(RELATION_SEMANTICS).toEqual(['structural', 'runtime', 'traffic', 'storage', 'network', 'failure-propagation', 'inferred'])
    expect(Object.values(RELATION_SEMANTIC_LABELS)).toEqual(['结构', '运行', '流量', '存储', '网络', '故障传播', '推断'])
    expect(relationSemantic(edgeOf('CONTAINS'), 'resource-relations')).toBe('structural')
    expect(relationSemantic(edgeOf('OWNS'), 'resource-relations')).toBe('structural')
    expect(relationSemantic(edgeOf('CONTROLS'), 'resource-relations')).toBe('runtime')
    expect(relationSemantic(edgeOf('RUNS_ON'), 'resource-relations')).toBe('runtime')
    expect(relationSemantic(edgeOf('ROUTES_TO'), 'resource-relations')).toBe('traffic')
    expect(relationSemantic(edgeOf('SELECTS'), 'resource-relations')).toBe('traffic')
    expect(relationSemantic(edgeOf('USES_VOLUME'), 'resource-relations')).toBe('storage')
    expect(relationSemantic(edgeOf('BOUND_TO'), 'resource-relations')).toBe('storage')
    expect(relationSemantic(edgeOf('CONNECTS_TO_NAD'), 'resource-relations')).toBe('network')
    expect(relationSemantic(edgeOf('USES_CNI'), 'resource-relations')).toBe('network')
    // 未知关系必须归入结构语义并保留原始名称，不得静默丢弃。
    expect(relationSemantic(edgeOf('SOMETHING_NEW'), 'resource-relations')).toBe('structural')
    expect(relationLabel('SOMETHING_NEW')).toBe('SOMETHING_NEW')
    // 故障传播只在 failure-chain 且事实边成立；推断优先级最高。
    expect(relationSemantic(edgeOf('CONTAINS', true), 'resource-relations')).toBe('structural')
    expect(relationSemantic(edgeOf('CONTAINS', true), 'failure-chain')).toBe('failure-propagation')
    expect(relationSemantic(edgeOf('CONTAINS', false, 'inferred'), 'failure-chain')).toBe('inferred')
  })

  it('focuses a selected node while keeping every relation reachable in the equivalent list', () => {
    const model = buildGraphDisplayModel(graph(4), { mode: 'resource-relations', centerUid: 'service:center', maxNodes: 80, maxEdges: 200 })
    const focused = focusGraph(model, { nodeId: 'service:center' })
    const direct = focused.edges.filter((edge) => edge.source === 'service:center' || edge.target === 'service:center')
    expect(direct.length).toBeGreaterThan(0)
    expect(direct.every((edge) => edge.dimmed === false)).toBe(true)
    // 等价关系列表不得因聚焦而丢失任何一条关系。
    expect(focused.relationRows).toHaveLength(model.relationRows.length)
    // 点击空白恢复全图。
    const restored = focusGraph(model, {})
    expect(restored.nodes.every((node) => !node.dimmed)).toBe(true)
  })

  it('focuses a selected edge together with both endpoints', () => {
    const model = buildGraphDisplayModel(graph(4), { mode: 'resource-relations', centerUid: 'service:center', maxNodes: 80, maxEdges: 200 })
    const targetEdge = model.edges[0]
    const focused = focusGraph(model, { edgeId: targetEdge.id })
    expect(focused.edges.find((edge) => edge.id === targetEdge.id)?.dimmed).toBe(false)
    expect(focused.nodes.find((node) => node.id === targetEdge.source)?.dimmed).toBe(false)
    expect(focused.nodes.find((node) => node.id === targetEdge.target)?.dimmed).toBe(false)
    expect(focused.relationRows).toHaveLength(model.relationRows.length)
  })

  it('keeps namespace and node as collapsed context until they are required', () => {
    expect(isContextEntityType('namespace')).toBe(true)
    expect(isContextEntityType('k8s_node')).toBe(true)
    expect(isContextEntityType('pod')).toBe(false)

    const input: GraphSubgraph = {
      center_entity_uid: 'pod:0',
      vertices: [
        { entity_uid: 'pod:0', entity_type: 'pod', tenant_id: 'tenant-a', cluster_id: 'cluster-a', name: 'api-0', name_key: 'api-0', source: 'test', status: 'active', confidence: 1, generation: 1, attrs_version: 1 },
        { entity_uid: 'k8s_node:0', entity_type: 'k8s_node', tenant_id: 'tenant-a', cluster_id: 'cluster-a', name: 'node-0', name_key: 'node-0', source: 'test', status: 'active', confidence: 1, generation: 1, attrs_version: 1 },
      ],
      edges: [],
      meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: false, stale: false, generated_at: '', warning_codes: [] },
    }
    const collapsed = buildGraphDisplayModel(input, { mode: 'resource-relations', centerUid: 'pod:0', maxNodes: 80, maxEdges: 200 })
    expect(collapsed.nodes.some((node) => node.id === 'k8s_node:0')).toBe(false)
    // 折叠的上下文节点进入聚合，而不是被静默丢弃。
    expect(collapsed.omittedByType.k8s_node).toBe(1)

    const expanded = buildGraphDisplayModel(input, { mode: 'resource-relations', centerUid: 'pod:0', maxNodes: 80, maxEdges: 200, expandedContextUids: new Set(['k8s_node:0']) })
    expect(expanded.nodes.some((node) => node.id === 'k8s_node:0')).toBe(true)
  })
})

function edgeOf(relationType: string, propagatesFailure = false, status = 'active'): GraphEdge {
  return {
    edge_uid: `edge:${relationType}`, source_uid: 'a', target_uid: 'b', relation_type: relationType,
    tenant_id: 'tenant-a', cluster_id: 'cluster-a', status, source: 'test', confidence: 1,
    generation: 1, propagates_failure: propagatesFailure, candidate_direction: 'forward',
    impact_direction: 'downstream', attrs_version: 1,
  }
}

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
