/**
 * 工作流状态中文投影。
 *
 * 内部 status code 绝不能裸显示给用户（验收 G7/G8）：所有调查、动作、执行与验证
 * 状态必须经过这里的稳定映射，未知值统一显示"未知"而不是把下划线替换成空格。
 */

const INVESTIGATION_STATUS_LABELS: Record<string, string> = {
  created: '已创建',
  planning: '规划中',
  investigating: '调查中',
  awaiting_confirmation: '待确认',
  awaiting_approval: '待审批',
  executing: '执行中',
  verifying: '验证中',
  success: '已完成',
  partial: '部分完成',
  failed: '失败',
  regressed: '发生回归',
  cancelled: '已取消',
}

const ACTION_STATUS_LABELS: Record<string, string> = {
  proposed: '待审批',
  pending: '待审批',
  approved: '已批准',
  rejected: '已拒绝',
  running: '执行中',
  queued: '排队中',
  succeeded: '已成功',
  success: '已成功',
  completed: '已完成',
  failed: '失败',
  partial: '部分完成',
  regressed: '发生回归',
  cancelled: '已取消',
  rolled_back: '已回滚',
  unknown: '未知',
}

const EXECUTION_STATUS_LABELS: Record<string, string> = {
  not_started: '未执行',
  queued: '排队中',
  running: '执行中',
  executing: '执行中',
  succeeded: '执行成功',
  success: '执行成功',
  completed: '执行完成',
  failed: '执行失败',
  partial: '部分执行',
  regressed: '发生回归',
  rolled_back: '已回滚',
  cancelled: '已取消',
  unknown: '未知',
}

const VERIFICATION_STATUS_LABELS: Record<string, string> = {
  pending: '待验证',
  pending_verification: '待验证',
  awaiting_verification: '待验证',
  verifying: '验证中',
  verified: '验证通过',
  passed: '验证通过',
  failed: '验证失败',
  partial: '部分通过',
  regressed: '验证发现回归',
  unknown: '未知',
}

const PREFLIGHT_STATUS_LABELS: Record<string, string> = {
  pending: '待预检',
  passed: '预检通过',
  ok: '预检通过',
  succeeded: '预检通过',
  failed: '预检失败',
  blocked: '预检受阻',
  skipped: '未预检',
  unknown: '未知',
}

export const investigationStatusLabel = (status: string): string => INVESTIGATION_STATUS_LABELS[status] ?? '未知'
export const preflightStatusLabel = (status: string): string => PREFLIGHT_STATUS_LABELS[status] ?? '未知'
export const actionStatusLabel = (status: string): string => ACTION_STATUS_LABELS[status] ?? '未知'
export const executionStatusLabel = (status: string): string => EXECUTION_STATUS_LABELS[status] ?? '未知'
export const verificationStatusLabel = (status: string): string => VERIFICATION_STATUS_LABELS[status] ?? '未知'

/** 调查终止原因：六个稳定值，`partial` 不是终止原因。 */
export type TerminationReason = 'root_confirmed' | 'evidence_exhausted' | 'budget_exhausted' | 'source_unavailable' | 'cancelled' | 'runtime_failed'

export const TERMINATION_REASON_LABELS: Record<TerminationReason, string> = {
  root_confirmed: '已取得足够证据并确认根因',
  evidence_exhausted: '可用证据已查询完，仍不足以确认',
  budget_exhausted: '已达到本次调查预算，以下为已取得证据',
  source_unavailable: '必要数据源不可用',
  cancelled: '调查已取消',
  runtime_failed: '调查运行失败',
}

export function terminationReasonLabel(reason: string): string {
  return TERMINATION_REASON_LABELS[reason as TerminationReason] ?? '终止原因未提供'
}

/** 调查来源：区分人工发起、系统建议、系统自动，不使用裸 "system"。 */
export type InvestigationSource = 'human' | 'system_suggested' | 'system_auto' | 'unknown'

const SOURCE_LABELS: Record<InvestigationSource, string> = {
  human: '人工发起',
  system_suggested: '系统建议',
  system_auto: '系统自动',
  unknown: '来源未知',
}

export function investigationSourceLabel(source: InvestigationSource): string {
  return SOURCE_LABELS[source] ?? SOURCE_LABELS.unknown
}
