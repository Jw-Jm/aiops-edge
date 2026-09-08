import type { ActionProjection } from '../../api/client'

export function canDecideAction(role: string): boolean {
  return role === 'approver' || role === 'admin'
}

export function toActionViewModel(action: ActionProjection & { risk_score?: number; risk_level?: string; impact_summary?: string; approval_status?: string; verification_status?: string; rollback_summary?: string }) {
  const riskLevel = action.risk_level || (action.risk_score == null ? 'unknown' : String(action.risk_score))
  return {
    actionId: action.action_id,
    runId: action.run_id,
    runHref: `/investigation/${action.run_id}`,
    operation: action.operation,
    target: `${action.target_resource_type}/${action.target_name}`,
    risk: { level: riskLevel, label: riskLevel === 'unknown' ? '未评估' : riskLevel },
    impact: action.impact_summary || '未提供',
    approvalStatus: action.approval_status || (action.status === 'proposed' ? '待审批' : action.status),
    executionStatus: action.execution_status || '未执行',
    verificationStatus: action.verification_status || '未验证',
    preflightStatus: action.preflight_status,
    resourceVersion: action.resource_version,
    actionVersion: action.action_version,
    actionHash: action.action_hash,
    rollback: action.rollback_summary || '未提供',
  }
}
