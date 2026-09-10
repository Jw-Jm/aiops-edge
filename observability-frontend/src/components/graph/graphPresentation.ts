import type { GraphEdge, GraphEntity, GraphMeta, GraphSubgraph } from '../../api/graphContracts'
import { resourceDomainOf, resourceTypeLabel } from '../../features/resources/resourceDomain'
import type { ResourceDomain } from '../../features/resources/types'

export type GraphViewMode = 'resource-relations' | 'failure-chain' | 'expert'
export type GraphLayoutMode = 'radial' | 'hierarchy-tb' | 'dag-lr'
export type GraphHealthTone = 'critical' | 'degraded' | 'risk' | 'healthy' | 'unknown'

export interface GraphDisplayNode {
  id: string
  name: string
  label: string
  entityType: string
  typeLabel: string
  domain: ResourceDomain | 'other'
  iconKey: string
  health: string
  healthTone: GraphHealthTone
  aggregate?: boolean
  omittedCount?: number
}

export interface GraphDisplayEdge {
  id: string
  source: string
  target: string
  relationType: string
  label: string
  style: 'failure' | 'structural' | 'inferred'
  propagatesFailure: boolean
  factStatus: 'fact' | 'inferred'
  sourceRef: string
  syncedAt: string
  aggregateCount?: number
}

export interface GraphRelationRow extends GraphDisplayEdge {
  sourceUid: string
  targetUid: string
  sourceName: string
  targetName: string
}

export interface GraphDisplayModel {
  nodes: GraphDisplayNode[]
  edges: GraphDisplayEdge[]
  omittedByType: Record<string, number>
  relationRows: GraphRelationRow[]
  meta: GraphMeta
}

const RELATION_LABELS: Record<string, string> = {
  CONTAINS: '包含', HOSTS: '宿主', RUNS_ON: '运行于', USES_VOLUME: '使用存储卷', USES_DISK: '使用磁盘设备', REFERENCES_VOLUME: '引用卷', SOURCED_FROM: '来源于', DECLARES: '声明 PVC', BOUND_TO: '绑定 PV', ATTACHED_TO: '挂载于', DEPENDS_ON: '依赖', CONNECTS_TO_NAD: '连接到 NAD', USES_CNI: '使用 CNI', CONNECTS_TO_NETWORK: '连接到网络',
}
const ICON_KEYS: Record<string, string> = { physical_server: 'physical-server', k8s_node: 'kubernetes-node', vm: 'virtual-machine' }

export function layoutForMode(mode: GraphViewMode): GraphLayoutMode {
  if (mode === 'resource-relations') return 'dag-lr'
  if (mode === 'failure-chain') return 'hierarchy-tb'
  return 'dag-lr'
}

export function relationLabel(relationType: string): string {
  return RELATION_LABELS[relationType] ?? (relationType || '关系')
}

export function resourceVisual(type: string): { iconKey: string; typeLabel: string; domain: ResourceDomain | 'other' } {
  return { iconKey: ICON_KEYS[type] ?? `resource-${type}`, typeLabel: resourceTypeLabel(type as never), domain: resourceDomainOf(type as never) ?? 'other' }
}

function healthTone(value: unknown): GraphHealthTone {
  const normalized = String(value ?? '').toLowerCase()
  if (normalized === 'critical' || normalized === 'fatal' || normalized === 'down') return 'critical'
  if (normalized === 'degraded' || normalized === 'warning' || normalized === 'unhealthy') return 'degraded'
  if (normalized === 'risk') return 'risk'
  if (normalized === 'healthy' || normalized === 'ok' || normalized === 'ready' || normalized === 'active') return 'healthy'
  return 'unknown'
}

function nodeFromEntity(entity: GraphEntity): GraphDisplayNode {
  const visual = resourceVisual(entity.entity_type)
  return { id: entity.entity_uid, name: entity.name, label: entity.name, entityType: entity.entity_type, typeLabel: visual.typeLabel, domain: visual.domain, iconKey: visual.iconKey, health: entity.health || entity.status, healthTone: healthTone(entity.health || entity.status) }
}

function edgeStyle(edge: GraphEdge): GraphDisplayEdge['style'] {
  if (edge.propagates_failure) return 'failure'
  if (edge.attrs?.fact_status === 'inferred' || edge.candidate_direction === 'candidate' || edge.status === 'candidate' || edge.status === 'inferred') return 'inferred'
  return 'structural'
}

function factStatus(edge: GraphEdge): GraphDisplayEdge['factStatus'] {
  return edge.attrs?.fact_status === 'inferred' || edge.status === 'candidate' || edge.status === 'inferred' ? 'inferred' : 'fact'
}

export function buildGraphDisplayModel(graph: GraphSubgraph, options: { mode: GraphViewMode; centerUid: string; maxNodes: number; maxEdges: number }): GraphDisplayModel {
  const maxNodes = Math.max(1, options.maxNodes)
  const maxEdges = Math.max(0, options.maxEdges)
  const center = graph.vertices.find((vertex) => vertex.entity_uid === options.centerUid) ?? graph.vertices.find((vertex) => vertex.entity_uid === graph.center_entity_uid)
  const candidates = graph.vertices.filter((vertex) => vertex.entity_uid !== center?.entity_uid)
  const omittedTypes = new Map<string, number>()
  const aggregateTypes = Array.from(new Set(candidates.map((vertex) => vertex.entity_type)))
  const centerSlots = center ? 1 : 0
  const aggregateSlots = Math.min(aggregateTypes.length, Math.max(0, maxNodes - centerSlots))
  const realSlots = Math.max(0, maxNodes - centerSlots - aggregateSlots)
  const visibleEntities = candidates.slice(0, realSlots)
  const visibleIds = new Set([...(center ? [center.entity_uid] : []), ...visibleEntities.map((entity) => entity.entity_uid)])
  candidates.slice(realSlots).forEach((entity) => omittedTypes.set(entity.entity_type, (omittedTypes.get(entity.entity_type) ?? 0) + 1))
  const nodes: GraphDisplayNode[] = [ ...(center ? [nodeFromEntity(center)] : []), ...visibleEntities.map(nodeFromEntity) ]
  const aggregateIds = new Map<string, string>()
  Array.from(omittedTypes.keys()).slice(0, aggregateSlots).forEach((type) => {
    const count = omittedTypes.get(type) ?? 0
    const id = `aggregate:${type}`
    aggregateIds.set(type, id)
    const visual = resourceVisual(type)
    nodes.push({ id, name: `还有 ${count} 个`, label: `还有 ${count} 个 ${visual.typeLabel}`, entityType: type, typeLabel: visual.typeLabel, domain: visual.domain, iconKey: visual.iconKey, health: 'unknown', healthTone: 'unknown', aggregate: true, omittedCount: count })
  })
  const nodeById = new Map(nodes.map((node) => [node.id, node]))
  const edgeMap = new Map<string, GraphDisplayEdge>()
  graph.edges.forEach((edge) => {
    const source = visibleIds.has(edge.source_uid) ? edge.source_uid : aggregateIds.get(graph.vertices.find((vertex) => vertex.entity_uid === edge.source_uid)?.entity_type ?? '')
    const target = visibleIds.has(edge.target_uid) ? edge.target_uid : aggregateIds.get(graph.vertices.find((vertex) => vertex.entity_uid === edge.target_uid)?.entity_type ?? '')
    if (!source || !target || source === target || !nodeById.has(source) || !nodeById.has(target)) return
    const display: GraphDisplayEdge = { id: `${edge.edge_uid}:${source}:${target}`, source, target, relationType: edge.relation_type, label: relationLabel(edge.relation_type), style: edgeStyle(edge), propagatesFailure: edge.propagates_failure, factStatus: factStatus(edge), sourceRef: typeof edge.attrs?.source_field === 'string' ? edge.attrs.source_field : edge.source, syncedAt: typeof edge.attrs?.synced_at === 'string' ? edge.attrs.synced_at : '', ...(typeof edge.attrs?.aggregate_count === 'number' ? { aggregateCount: edge.attrs.aggregate_count } : {}) }
    edgeMap.set(`${source}|${target}|${edge.relation_type}`, display)
  })
  const edges = Array.from(edgeMap.values()).slice(0, maxEdges)
  const names = new Map(nodes.map((node) => [node.id, node.name]))
  const relationRows = edges.map((edge) => ({ ...edge, sourceUid: edge.source, targetUid: edge.target, sourceName: names.get(edge.source) ?? edge.source, targetName: names.get(edge.target) ?? edge.target }))
  return { nodes, edges, omittedByType: Object.fromEntries(omittedTypes), relationRows, meta: graph.meta }
}
