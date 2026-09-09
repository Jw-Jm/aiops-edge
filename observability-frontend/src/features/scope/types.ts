import type { PlatformResourceRef } from '../resources/types'

export type RelativeMinutes = 15 | 60 | 360 | 1440

export type TimeRange =
  | { mode: 'relative'; minutes: RelativeMinutes }
  | { mode: 'absolute'; start: string; end: string }

export interface ActiveScope {
  tenantId: string
  clusterId: string
  resource?: PlatformResourceRef
  timeRange: TimeRange
}

export interface RunScopeSnapshot extends ActiveScope {
  mode: 'snapshot'
  runId: string
  timeRange: Extract<TimeRange, { mode: 'absolute' }>
}

export const DEFAULT_ACTIVE_SCOPE: ActiveScope = {
  tenantId: '',
  clusterId: '',
  timeRange: { mode: 'relative', minutes: 60 },
}

export function formatTimeRange(range: TimeRange): string {
  if (range.mode === 'relative') return `最近 ${range.minutes >= 60 ? `${range.minutes / 60} 小时` : `${range.minutes} 分钟`}`
  const start = range.start.slice(11, 16)
  const end = range.end.slice(11, 16)
  return `${start}–${end}`
}
