export type DraftSource = 'overview' | 'alert' | 'resource' | 'chat'

export interface InvestigationDraft {
  source: DraftSource
  clusterId: string
  namespace?: string
  resourceId: string
  targetType: string
  symptom: string
  problemId?: string
  actionMode?: 'read_only' | 'propose'
}

const SOURCES = new Set<DraftSource>(['overview', 'alert', 'resource', 'chat'])

export function draftToSearchParams(draft: InvestigationDraft): URLSearchParams {
  const params = new URLSearchParams()
  Object.entries(draft).forEach(([key, value]) => { if (value) params.set(key, String(value)) })
  return params
}

export function draftFromSearchParams(input: URLSearchParams | Record<string, string | undefined>): InvestigationDraft {
  const get = (key: string) => input instanceof URLSearchParams ? input.get(key) || '' : input[key] || ''
  const source = SOURCES.has(get('source') as DraftSource) ? get('source') as DraftSource : 'overview'
  const actionMode = get('actionMode')
  return {
    source,
    clusterId: get('clusterId'),
    namespace: get('namespace') || undefined,
    resourceId: get('resource') || get('resourceId') || get('service'),
    targetType: get('targetType') || 'service',
    symptom: get('symptom'),
    ...(get('problem_id') ? { problemId: get('problem_id') } : {}),
    ...(actionMode === 'propose' || actionMode === 'read_only' ? { actionMode } : {}),
  }
}
