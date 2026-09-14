/**
 * AI 智能运维结论等级 view model（设计规范 §6.1 / §11.3）
 *
 * 结论等级固定四级：
 *   Unknown   证据不足，当前无法定位
 *   Candidate 可能原因之一，存在明显缺口
 *   Supported 最可能原因；多源证据、时间一致、关键反证已检查
 *   Confirmed 已确认根因；可重复验证或处置后恢复闭环，且无关键反证
 *
 * 硬约束（S1/S0 级）：证据不足、证据冲突、来源 Partial/Stale 或关键来源不可用时
 * 一律不得输出 Confirmed。该判定必须是纯函数并被单元测试覆盖。
 */

export type ConclusionLevel = 'Unknown' | 'Candidate' | 'Supported' | 'Confirmed'

export const CONCLUSION_LEVELS: readonly ConclusionLevel[] = ['Unknown', 'Candidate', 'Supported', 'Confirmed']

export const CONCLUSION_LABELS: Record<ConclusionLevel, string> = {
  Unknown: '未知',
  Candidate: '候选',
  Supported: '支持',
  Confirmed: '已确认',
}

export const CONCLUSION_DESCRIPTIONS: Record<ConclusionLevel, string> = {
  Unknown: '证据不足，当前无法定位；已列出缺失证据与下一步',
  Candidate: '可能原因之一；存在明显证据缺口',
  Supported: '最可能原因；多源证据时间一致，关键反证已检查',
  Confirmed: '已确认根因；处置后恢复形成闭环且无关键反证',
}

export interface ConclusionInput {
  /** 服务端给出的根因描述；为空表示尚未定位 */
  rootCause?: string | null
  /** 服务端置信度 0-1；为空表示未提供 */
  confidence?: number | null
  /** 已入库证据条数 */
  evidenceCount: number
  /** 关键来源不可用（unavailable / not connected） */
  hasUnavailableSource: boolean
  /** 数据 Partial（部分来源失败） */
  partial: boolean
  /** 产生结论所依赖的数据已过期（stale） */
  stale: boolean
  /** 存在未解决的关键反证 */
  hasUnresolvedContradiction: boolean
  /** 关键断言缺少直接证据 */
  hasDirectEvidence: boolean
  /** Run 是否处于终态 */
  terminal: boolean
}

export interface ConclusionView {
  level: ConclusionLevel
  /** 为什么不是更高等级：用于 UI 的"降级原因" */
  downgradeReasons: string[]
  /** 提高等级还缺什么 */
  missing: string[]
}

const SUPPORTED_THRESHOLD = 0.6
const CONFIRMED_THRESHOLD = 0.8

/**
 * 依据真实 Run 事实推导结论等级。
 * 任何降级条件成立时都必须落到更低等级，不得由置信度数字单独决定。
 */
export function deriveConclusion(input: ConclusionInput): ConclusionView {
  const reasons: string[] = []
  const missing: string[] = []

  if (!input.rootCause || input.rootCause.trim() === '') {
    missing.push('尚未形成候选根因')
    return {
      level: 'Unknown',
      downgradeReasons: [],
      missing: input.evidenceCount === 0 ? [...missing, '没有任何已入库证据'] : missing,
    }
  }

  if (input.evidenceCount === 0) {
    return { level: 'Unknown', downgradeReasons: ['结论没有任何已入库证据'], missing: ['需要至少一项可打开证据'] }
  }

  if (input.hasUnavailableSource) {
    reasons.push('存在不可用数据来源，无法排除其它可能')
  }
  if (input.partial) reasons.push('部分来源数据不完整')
  if (input.stale) reasons.push('依赖数据已过期（stale）')
  if (input.hasUnresolvedContradiction) reasons.push('存在未解决的关键反证')

  const confidence = typeof input.confidence === 'number' && Number.isFinite(input.confidence) ? input.confidence : null
  if (confidence === null) missing.push('服务端未给出置信度')

  // 冲突 / 不可用 / 陈旧 → 最多 Candidate
  if (reasons.length > 0) {
    return {
      level: 'Candidate',
      downgradeReasons: reasons,
      missing: [...missing, '需补齐或刷新关键来源并复核反证'],
    }
  }

  if (confidence === null) {
    return { level: 'Candidate', downgradeReasons: [], missing }
  }

  if (confidence < SUPPORTED_THRESHOLD) {
    return { level: 'Candidate', downgradeReasons: [], missing: [...missing, '置信度低于支持等级门槛'] }
  }

  if (confidence < CONFIRMED_THRESHOLD) {
    return { level: 'Supported', downgradeReasons: [], missing: [...missing, '置信度未达到确认门槛'] }
  }

  if (!input.hasDirectEvidence) {
    return { level: 'Supported', downgradeReasons: ['缺少直接支持证据'], missing: [...missing, '需要可复现的直接证据'] }
  }

  if (!input.terminal) {
    return { level: 'Supported', downgradeReasons: [], missing: [...missing, '调查仍在进行，尚未形成恢复闭环'] }
  }

  return { level: 'Confirmed', downgradeReasons: [], missing }
}

/** 结论等级是否允许触发受控动作草稿；Unknown 不允许 */
export function allowsActionDraft(level: ConclusionLevel): boolean {
  return level === 'Supported' || level === 'Confirmed'
}

export type ActionStage = 'Draft' | 'Preflight' | 'AwaitingConfirmation' | 'Running' | 'Succeeded' | 'Failed' | 'Verified' | 'RolledBack'

export const ACTION_STAGE_LABELS: Record<ActionStage, string> = {
  Draft: '草稿',
  Preflight: '预检',
  AwaitingConfirmation: '待确认',
  Running: '执行中',
  Succeeded: '执行成功',
  Failed: '执行失败',
  Verified: '已验证恢复',
  RolledBack: '已回滚',
}

/** 服务端动作状态 → 本页统一生命周期（不显示"未知/待验证"伪状态） */
export function projectActionStage(action: {
  status?: string | null
  preflight_status?: string | null
  execution_status?: string | null
  verification_status?: string | null
  approval_status?: string | null
}): ActionStage {
  const status = (action.status ?? '').toLowerCase()
  const execution = (action.execution_status ?? '').toLowerCase()
  const verification = (action.verification_status ?? '').toLowerCase()
  const preflight = (action.preflight_status ?? '').toLowerCase()

  if (status === 'rejected') return 'Draft'
  if (verification === 'verified' || verification === 'recovered') return 'Verified'
  if (execution === 'failed' || execution === 'error') return 'Failed'
  if (execution === 'rolled_back' || execution === 'rolledback') return 'RolledBack'
  if (execution === 'running' || execution === 'executing' || execution === 'in_progress') return 'Running'
  if (execution === 'succeeded' || execution === 'success') return 'Succeeded'
  if (status === 'approved') return 'Preflight'
  if (preflight === 'passed' && status !== 'approved') return 'AwaitingConfirmation'
  if (preflight === 'failed') return 'Draft'
  return 'Draft'
}
