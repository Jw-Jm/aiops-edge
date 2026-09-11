import { buildInvestigationSummary, type InvestigationSummary, type InvestigationSummaryInput } from './summary'
import { resourceDomainOf } from '../resources/resourceDomain'
import type { PlatformResourceRef } from '../resources/types'

export interface InvestigationEvidenceInput {
  evidence_id?: string
  observed_at?: string | null
  type?: string
  source?: string
  fact?: string
  source_reliability?: number | null
  quality?: string
  supports?: string[]
  contradicts?: string[]
}

export interface InvestigationSnapshotInput {
  run_id: string
  tenant_id?: string
  primary_cluster_id?: string
  target_resource_id?: string | null
  target_resource_type?: string | null
  intent?: string
  status?: string
  root_cause?: string | null
  confidence?: number | null
  evidence?: InvestigationEvidenceInput[]
  hypotheses?: Array<{ hypothesis_id?: string; content?: string; confidence?: number; missing_evidence?: string[]; contradicting_evidence?: string[] }>
  action?: { status?: string; risk?: string; execution?: string | null; verification?: string | null }
  created_at?: string | null
  environment?: string | null
  namespace?: string | null
  query_window_start?: string | null
  query_window_end?: string | null
  /** 服务端 investigation_summary（因果链/预算/终止原因）。 */
  investigation_summary?: Record<string, unknown> | null
  summary_input?: InvestigationSummaryInput
  partial?: boolean
  stale?: boolean
}

export interface InvestigationViewModel {
  runId: string
  scope: { tenantId: string; clusterId: string; resourceId: string; resource?: PlatformResourceRef; timeRange?: { mode: 'absolute'; start: string; end: string } }
  intent: string
  status: string
  evidence: Array<{ id: string; observedAt: string; type: string; source: string; fact: string; reliability: number | null; quality: string; supports: string[]; contradicts: string[] }>
  hypotheses: Array<{ id: string; claim: string; confidence: number; missing: string[]; contradicts: string[] }>
  conclusion: { state: 'confirmed' | 'insufficient_evidence' | 'unknown'; title: string; confidence: number; rootCause: string }
  /** 结论优先摘要：因果链、证据因子、终止原因与预算。 */
  summary: InvestigationSummary
  action?: { status: string; risk: string; execution?: string | null; verification?: string | null }
}

export function toInvestigationViewModel(snapshot: InvestigationSnapshotInput): InvestigationViewModel {
  const confidence = Number(snapshot.confidence ?? 0)
  const evidence = (snapshot.evidence ?? []).map((item, index) => ({
    id: String(item.evidence_id ?? `evidence-${index}`),
    observedAt: String(item.observed_at ?? ''),
    type: String(item.type ?? 'unknown'),
    source: String(item.source ?? 'unknown'),
    fact: String(item.fact ?? ''),
    reliability: item.source_reliability == null ? null : Number(item.source_reliability),
    quality: String(item.quality ?? 'unknown'),
    supports: item.supports ?? [],
    contradicts: item.contradicts ?? [],
  })).sort((a, b) => b.observedAt.localeCompare(a.observedAt))
  const hypotheses = (snapshot.hypotheses ?? []).map((item, index) => ({
    id: String(item.hypothesis_id ?? `hypothesis-${index}`),
    claim: String(item.content ?? ''),
    confidence: Number(item.confidence ?? 0),
    missing: item.missing_evidence ?? [],
    contradicts: item.contradicting_evidence ?? [],
  }))
  const rootCause = String(snapshot.root_cause ?? '')
  const insufficient = !rootCause || confidence < 0.8 || evidence.length === 0
  const resourceType = snapshot.target_resource_type || ''
  const resourceDomain = resourceDomainOf(resourceType)
  // Task 11：服务端持久化的 investigation_summary 是唯一可信来源；
  // 前端不从英文内部状态猜终止原因。
  const serverSummary = snapshot.investigation_summary ?? null
  const summary = buildInvestigationSummary({
    ...(snapshot.root_cause !== undefined ? { root_cause: snapshot.root_cause } : {}),
    confidence,
    status: snapshot.status ?? 'created',
    evidence: snapshot.evidence ?? [],
    hypotheses: snapshot.hypotheses ?? [],
    partial: snapshot.partial === true,
    stale: snapshot.stale === true,
    ...(serverSummary ? {
      ...(serverSummary.termination_reason ? { termination_reason: String(serverSummary.termination_reason) } : {}),
      ...(serverSummary.budget_summary ? { budget_summary: serverSummary.budget_summary as InvestigationSummaryInput['budget_summary'] } : {}),
      ...(Array.isArray(serverSummary.causal_chain) ? { causal_chain: serverSummary.causal_chain as InvestigationSummaryInput['causal_chain'] } : {}),
      ...(Array.isArray(serverSummary.next_verification) ? { next_verification: serverSummary.next_verification as string[] } : {}),
    } : {}),
    ...(snapshot.summary_input ?? {}),
  })
  return {
    runId: snapshot.run_id,
    scope: {
      tenantId: snapshot.tenant_id ?? '',
      clusterId: snapshot.primary_cluster_id ?? '',
      resourceId: snapshot.target_resource_id ?? 'investigation',
      ...(snapshot.target_resource_id && snapshot.primary_cluster_id && resourceDomain ? {
        resource: {
          clusterId: snapshot.primary_cluster_id,
          uid: snapshot.target_resource_id,
          type: resourceType,
          domain: resourceDomain,
          name: snapshot.target_resource_id,
          ...(snapshot.namespace && resourceDomain === 'kubernetes' ? { namespace: snapshot.namespace } : {}),
        },
      } : {}),
      timeRange: snapshot.query_window_start && snapshot.query_window_end ? { mode: 'absolute', start: snapshot.query_window_start, end: snapshot.query_window_end } : undefined,
    },
    intent: snapshot.intent ?? '—', status: snapshot.status ?? 'created', evidence, hypotheses,
    conclusion: { state: insufficient ? 'insufficient_evidence' : 'confirmed', title: insufficient ? '证据不足，尚不能确认根因' : rootCause, confidence, rootCause: insufficient ? '' : rootCause },
    // 摘要必须与 conclusion 一致：partial/stale/缺少 eligible evidence 一律不得显示"根因已确认"。
    summary: { ...summary, conclusionState: insufficient ? 'insufficient_evidence' : summary.conclusionState, rootCause: insufficient ? null : summary.rootCause, evidenceFactors: { ...summary.evidenceFactors, partial: summary.evidenceFactors.partial || snapshot.partial === true, stale: summary.evidenceFactors.stale || snapshot.stale === true } },
    ...(snapshot.action ? { action: { status: snapshot.action.status ?? 'unknown', risk: snapshot.action.risk ?? 'unknown', execution: snapshot.action.execution, verification: snapshot.action.verification } } : {}),
  }
}

/** Immutable investigation boundary carried from the persisted Run. */
export interface FrozenInvestigationScope {
  tenantId: string
  clusterId: string
  resourceUid?: string
  from: string
  to: string
}
