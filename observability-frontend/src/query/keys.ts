export interface QueryContextKey {
  tenantId: string
  activeClusterId: string
  namespace?: string
  entityUid?: string
  from?: string
  to?: string
}

function stableFilters(filters: Record<string, unknown>): string {
  return JSON.stringify(Object.entries(filters)
    .filter(([, value]) => value !== undefined && value !== '')
    .sort(([left], [right]) => left.localeCompare(right)))
}

function scope(context: QueryContextKey): Array<string | undefined> {
  return [context.tenantId, context.activeClusterId, context.namespace ?? '-', context.entityUid ?? '-', context.from ?? '-', context.to ?? '-']
}

export const queryKeys = {
  services: (context: QueryContextKey) => ['services', ...scope(context)] as const,
  problems: (context: QueryContextKey) => ['problems', ...scope(context)] as const,
  investigations: (context: QueryContextKey) => ['investigations', ...scope(context)] as const,
  actions: (context: QueryContextKey) => ['actions', ...scope(context)] as const,
  resources: (context: QueryContextKey, kind?: string) => ['resources', kind ?? 'all', ...scope(context)] as const,
  resourceCatalog: (context: QueryContextKey, filters: Record<string, unknown> = {}) => ['resource-catalog', stableFilters(filters), ...scope(context)] as const,
  resourceSummary: (context: QueryContextKey) => ['resource-summary', ...scope(context)] as const,
  resourceDetail: (context: QueryContextKey, entityUid: string) => ['resource-detail', entityUid, ...scope({ ...context, entityUid })] as const,
  resourceGraph: (context: QueryContextKey, entityUid: string, mode: string, depth: number, domains: string[], relations: string[]) => [
    'resource-graph', entityUid, mode, depth, [...domains].sort().join(','), [...relations].sort().join(','), ...scope({ ...context, entityUid }),
  ] as const,
  knowledgeList: (clusterId: string, filters: Record<string, unknown> = {}) => ['knowledge', clusterId, 'list', stableFilters(filters)] as const,
  knowledgeDetail: (clusterId: string, knowledgeId: string) => ['knowledge', clusterId, 'detail', knowledgeId] as const,
  knowledgeIndexStatus: (clusterId: string) => ['knowledge', clusterId, 'index-status'] as const,
  assistantSession: (clusterId: string, sessionId: string) => ['assistant', clusterId, 'session', sessionId] as const,
  assistantAnswer: (clusterId: string, sessionId: string, turnId: string) => ['assistant', clusterId, 'answer', sessionId, turnId] as const,
}
