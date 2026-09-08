export interface QueryContextKey {
  tenantId: string
  activeClusterId: string
  namespace?: string
  entityUid?: string
  from?: string
  to?: string
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
}

