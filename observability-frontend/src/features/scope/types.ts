export type Environment = 'prod' | 'staging' | 'test' | 'dev' | 'unknown'
export type RelativeMinutes = 15 | 60 | 360 | 1440

export type TimeRange =
  | { mode: 'relative'; minutes: RelativeMinutes }
  | { mode: 'absolute'; start: string; end: string }

export interface ResourceRef {
  type: string
  id: string
  label: string
}

export interface ScopeContext {
  environment: Environment
  namespace: string
  resource?: ResourceRef
  timeRange: TimeRange
}

export interface RunScopeSnapshot extends Omit<ScopeContext, 'timeRange'> {
  mode: 'snapshot'
  runId: string
  tenantId: string
  clusterId: string
  timeRange?: TimeRange
}

export const DEFAULT_SCOPE_CONTEXT: ScopeContext = {
  environment: 'prod',
  namespace: '',
  resource: undefined,
  timeRange: { mode: 'relative', minutes: 60 },
}

export function formatTimeRange(range: TimeRange): string {
  if (range.mode === 'relative') return `最近 ${range.minutes >= 60 ? `${range.minutes / 60} 小时` : `${range.minutes} 分钟`}`
  const start = range.start.slice(11, 16)
  const end = range.end.slice(11, 16)
  return `${start}–${end}`
}
