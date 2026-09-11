export type Health = 'healthy' | 'degraded' | 'abnormal' | 'unknown'
export type DataStatus = 'available' | 'partial' | 'unavailable' | 'stale'

export type ActionPhase =
  | 'awaiting_approval'
  | 'ready_to_execute'
  | 'executing'
  | 'awaiting_verification'
  | 'completed'
  | 'failed'
  | 'regressed'

import type { PlatformResourceRef } from '../features/resources/types'
import type { AlertInvestigationLink } from '../api/client'

export interface ProblemSummary {
  problem_id: string
  severity: string
  health: Health
  title: string
  source_refs: string[]
  primary_resource?: string | null
  resource?: PlatformResourceRef | null
  cluster_id?: string | null
  affected_resources: string[]
  started_at?: string | null
  duration_seconds?: number | null
  evidence_summary: string[]
  recent_change?: string | null
  data_status: DataStatus
  active_run_id?: string | null
  action_phase?: ActionPhase | null
  /** Server-projected resource health facts. Optional so older API rows remain explicit. */
  failure_rate?: number | null
  ready_replicas?: number | null
  desired_replicas?: number | null
  /** Task 10：告警 → 调查的受控投影（来自服务端 investigation_link）。 */
  investigation?: AlertInvestigationLink | null
}

export interface FrozenRunContext {
  tenant_id: string
  primary_cluster_id: string
  target_type?: string | null
  target_resource_id?: string | null
  time_range_start: string
  time_range_end: string
}

export interface RunSummaryReadModel {
  run_id: string
  status: string
  intent: string
  frozen_context: FrozenRunContext
  root_cause?: string | null
  confidence?: number | null
  evidence_count: number
  action_phase?: ActionPhase | null
  created_at: string
  updated_at: string
}

export interface ActionSummaryReadModel {
  action_id: string
  run_id: string
  target: string
  operation: string
  authoritative_risk: string
  preflight_status: string
  approval_status: string
  execution_status: string
  verification_status: string
  action_version: number
  resource_version: string
  created_at: string
  updated_at: string
}

export function actionPhase(action: Partial<ActionSummaryReadModel>): ActionPhase {
  if (action.verification_status === 'regressed') return 'regressed'
  if (action.execution_status === 'failed') return 'failed'
  if (action.verification_status === 'success') return 'completed'
  if (action.execution_status === 'success' && action.verification_status === 'pending') return 'awaiting_verification'
  if (action.execution_status === 'queued' || action.execution_status === 'running') return 'executing'
  if (action.approval_status === 'approved' && (!action.execution_status || action.execution_status === 'not_started')) return 'ready_to_execute'
  return 'awaiting_approval'
}

export function healthLabel(value: string | null | undefined): string {
  switch (value) {
    case 'healthy': return '健康'
    case 'degraded': return '降级'
    case 'abnormal': return '异常'
    default: return '未知'
  }
}

export function dataStatusLabel(value: string | null | undefined): string {
  switch (value) {
    case 'available': return '数据正常'
    case 'partial': return '部分数据'
    case 'stale': return '数据陈旧'
    case 'unavailable': return '数据不可用'
    default: return '数据未知'
  }
}
