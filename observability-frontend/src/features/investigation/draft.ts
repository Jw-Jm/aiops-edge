import { resourceDomainOf } from '../resources/resourceDomain'
import type { PlatformResourceRef } from '../resources/types'

export type DraftSource = 'overview' | 'alert' | 'resource' | 'chat'

export interface InvestigationDraft {
  source: DraftSource
  clusterId: string
  namespace?: string
  resourceId?: string
  targetType: string
  symptom: string
  resource?: PlatformResourceRef
  problemId?: string
  actionMode?: 'read_only' | 'propose'
}

const SOURCES = new Set<DraftSource>(['overview', 'alert', 'resource', 'chat'])

export function draftToSearchParams(draft: InvestigationDraft): URLSearchParams {
  const params = new URLSearchParams()
  Object.entries(draft).forEach(([key, value]) => {
    if (key === 'resource' || value === undefined || value === '') return
    params.set(key, String(value))
  })
  if (draft.resource) {
    params.set('resource', draft.resource.uid)
    params.set('resourceType', draft.resource.type)
    params.set('resourceName', draft.resource.name)
    if (draft.resource.namespace) params.set('namespace', draft.resource.namespace)
  }
  return params
}

export function draftFromSearchParams(input: URLSearchParams | Record<string, string | undefined>): InvestigationDraft {
  const get = (key: string) => input instanceof URLSearchParams ? input.get(key) || '' : input[key] || ''
  const source = SOURCES.has(get('source') as DraftSource) ? get('source') as DraftSource : 'overview'
  const actionMode = get('actionMode')
  const resourceId = get('resource') || get('resourceId') || get('service')
  const resourceType = get('resourceType')
  const targetType = resourceType || get('targetType') || 'service'
  const resourceDomain = resourceDomainOf(resourceType as never)
  const resource = resourceId && get('clusterId') && resourceType && resourceDomain
    ? {
        clusterId: get('clusterId'), uid: resourceId, type: resourceType as never, domain: resourceDomain,
        name: get('resourceName') || resourceId,
        ...(resourceDomain === 'kubernetes' && get('namespace') ? { namespace: get('namespace') } : {}),
      } satisfies PlatformResourceRef
    : undefined
  return {
    source,
    clusterId: get('clusterId'),
    namespace: get('namespace') || undefined,
    ...(resourceId ? { resourceId } : {}),
    targetType,
    symptom: get('symptom'),
    ...(resource ? { resource } : {}),
    ...(get('problem_id') ? { problemId: get('problem_id') } : {}),
    ...(actionMode === 'propose' || actionMode === 'read_only' ? { actionMode } : {}),
  }
}
