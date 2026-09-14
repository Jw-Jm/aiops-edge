import type { AlertInvestigationLink } from '../../api/client'

/**
 * 告警调查状态机投影（Task 10 / 验收 G5）。
 * 本模块只做显示投影，不推导授权、不推导自动调查资格。
 */

const REASON_LABELS: Record<string, string> = {
  policy_manual: '当前集群策略为人工发起',
  below_severity: '严重度不足',
  concurrency_limited: '并发受限',
  rate_limited: '频率受限',
  invalid_scope: '范围无效',
  persistence_unavailable: '持久化不可用',
}

export const alertInvestigationReasonLabel = (code?: string): string =>
  code ? (REASON_LABELS[code] ?? code) : ''

export type AlertInvestigationAction = 'create' | 'start' | 'open' | 'view' | 'manual_create' | null

export interface AlertInvestigationView {
  label: string
  tone: 'default' | 'processing' | 'success' | 'warning' | 'error'
  action: AlertInvestigationAction
  reason: string
}

const STATUS_LABELS: Record<string, string> = {
  none: '未创建调查',
  draft: '调查草稿',
  run: '调查中',
  completed: '已完成',
  skipped: '已跳过',
}

/** 告警列表的调查状态与主动作必须一致，不得同时出现"未创建调查"和"打开调查"。 */
export function alertInvestigationView(link?: AlertInvestigationLink | null): AlertInvestigationView {
  const status = link?.status ?? 'none'
  const label = STATUS_LABELS[status] ?? '未创建调查'
  const reason = alertInvestigationReasonLabel(link?.reason_code)
  switch (status) {
    case 'draft':
      return { label, tone: 'processing', action: 'start', reason }
    case 'run':
      return { label, tone: 'processing', action: 'open', reason }
    case 'completed':
      return { label, tone: 'success', action: 'view', reason }
    case 'skipped':
      // 并发/频率受限仍允许人工发起，但不允许静默自动重试。
      return { label, tone: 'warning', action: 'manual_create', reason }
    default:
      return { label, tone: 'default', action: 'create', reason }
  }
}

export const ALERT_INVESTIGATION_READONLY_NOTICE = '自动调查仅只读，不会执行处置'
