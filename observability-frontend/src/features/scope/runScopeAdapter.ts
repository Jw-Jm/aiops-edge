import type { ActiveScope, TimeRange } from './types'

export interface LegacyRunScope {
  environment: 'prod'
  cluster_id: string
  namespace?: string
  target_resource_id?: string
  target_resource_type?: string
  time_start: string
  time_end: string
}

function absoluteWindow(range: TimeRange, now: Date): { start: string; end: string } {
  if (range.mode === 'absolute') return { start: range.start, end: range.end }
  const end = now.getTime()
  const start = end - range.minutes * 60_000
  return { start: new Date(start).toISOString(), end: now.toISOString() }
}

export function toLegacyRunScope(scope: ActiveScope, now = new Date()): LegacyRunScope {
  const window = absoluteWindow(scope.timeRange, now)
  return {
    environment: 'prod',
    cluster_id: scope.clusterId,
    ...(scope.resource?.domain === 'kubernetes' && scope.resource.namespace ? { namespace: scope.resource.namespace } : {}),
    ...(scope.resource ? { target_resource_id: scope.resource.uid, target_resource_type: scope.resource.type } : {}),
    time_start: window.start,
    time_end: window.end,
  }
}
