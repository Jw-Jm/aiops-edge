import type { TerminationReason } from '../workflow/statusPresentation'

export interface CausalChainStep {
  source: string
  relation: string
  target: string
  factStatus: 'fact' | 'inferred'
  evidenceIds?: string[]
}

export interface InvestigationEvidenceFactors {
  supporting: number
  contradicting: number
  missing: number
  partial: boolean
  stale: boolean
}

export interface InvestigationBudget {
  maxSteps: number
  maxTools: number
  consumedSteps: number
  consumedTools: number
}

export interface InvestigationSummary {
  conclusionState: 'confirmed' | 'insufficient_evidence'
  rootCause: string | null
  causalChain: CausalChainStep[]
  evidenceFactors: InvestigationEvidenceFactors
  terminationReason: TerminationReason
  budget: InvestigationBudget
  nextVerification: string[]
}

export interface InvestigationSummaryInput {
  root_cause?: string | null
  confidence?: number | null
  partial?: boolean
  stale?: boolean
  evidence?: Array<{ evidence_id?: string; contradicts?: string[]; supports?: string[] }>
  hypotheses?: Array<{ missing_evidence?: string[]; contradicting_evidence?: string[] }>
  causal_chain?: Array<{ source_uid?: string; source_name?: string; relation_type?: string; relation_label?: string; target_uid?: string; target_name?: string; fact_status?: string; evidence_ids?: string[] }>
  termination_reason?: string | null
  budget_summary?: { max_steps?: number; max_tools?: number; consumed_steps?: number; consumed_tools?: number }
  next_verification?: string[]
  status?: string | null
}

const TERMINAL_RUN_STATUSES = new Set(['success', 'partial', 'failed', 'regressed', 'cancelled'])

/**
 * 推导终止原因。以后端持久化的 `termination_reason` 为准；
 * 后端尚未提供时只按明确的运行状态回退，绝不用 `partial` 充当原因文本。
 */
export function resolveTerminationReason(input: InvestigationSummaryInput): TerminationReason {
  const explicit = String(input.termination_reason ?? '')
  if (explicit) return explicit as TerminationReason
  const status = String(input.status ?? '')
  if (status === 'success') return 'root_confirmed'
  if (status === 'cancelled') return 'cancelled'
  if (status === 'failed' || status === 'regressed') return 'runtime_failed'
  if (status === 'partial') return 'evidence_exhausted'
  return 'evidence_exhausted'
}

/** 只有终态 Run 才展示终止原因，运行中不展示。 */
export function isTerminalRunStatus(status: string): boolean {
  return TERMINAL_RUN_STATUSES.has(status)
}

export function buildInvestigationSummary(input: InvestigationSummaryInput): InvestigationSummary {
  const confidence = Number(input.confidence ?? 0)
  const evidence = input.evidence ?? []
  const partial = input.partial === true
  const stale = input.stale === true
  const rootCause = String(input.root_cause ?? '')
  const contradicting = new Set<string>()
  const supporting = new Set<string>()
  evidence.forEach((item) => {
    ;(item.contradicts ?? []).forEach((id) => contradicting.add(id))
    ;(item.supports ?? []).forEach((id) => supporting.add(id))
  })
  const missing = new Set<string>()
  ;(input.hypotheses ?? []).forEach((hypothesis) => {
    ;(hypothesis.missing_evidence ?? []).forEach((id) => missing.add(id))
    ;(hypothesis.contradicting_evidence ?? []).forEach((id) => contradicting.add(id))
  })

  // 确认门槛固定：存在根因 + confidence ≥ 0.8 + 至少一条 eligible evidence + 非 partial + 非 stale。
  const confirmed = Boolean(rootCause) && confidence >= 0.8 && evidence.length > 0 && !partial && !stale

  const causalChain: CausalChainStep[] = (input.causal_chain ?? [])
    .filter((step) => step.source_uid && step.target_uid)
    .slice(0, 12)
    .map((step) => ({
      source: step.source_name || step.source_uid || '',
      relation: step.relation_label || step.relation_type || '关系',
      target: step.target_name || step.target_uid || '',
      factStatus: step.fact_status === 'inferred' ? 'inferred' : 'fact',
      ...(step.evidence_ids ? { evidenceIds: step.evidence_ids } : {}),
    }))

  const budget: InvestigationBudget = {
    maxSteps: Number(input.budget_summary?.max_steps ?? 0),
    maxTools: Number(input.budget_summary?.max_tools ?? 0),
    consumedSteps: Number(input.budget_summary?.consumed_steps ?? 0),
    consumedTools: Number(input.budget_summary?.consumed_tools ?? 0),
  }

  return {
    conclusionState: confirmed ? 'confirmed' : 'insufficient_evidence',
    rootCause: confirmed ? rootCause : null,
    causalChain,
    evidenceFactors: { supporting: supporting.size, contradicting: contradicting.size, missing: missing.size, partial, stale },
    terminationReason: resolveTerminationReason(input),
    budget,
    nextVerification: input.next_verification ?? [],
  }
}
