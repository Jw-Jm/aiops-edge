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
}

export interface InvestigationViewModel {
  runId: string
  scope: { tenantId: string; clusterId: string; resourceId: string; environment: string; namespace: string; timeRange?: { mode: 'absolute'; start: string; end: string } }
  intent: string
  status: string
  evidence: Array<{ id: string; observedAt: string; type: string; source: string; fact: string; reliability: number | null; quality: string; supports: string[]; contradicts: string[] }>
  hypotheses: Array<{ id: string; claim: string; confidence: number; missing: string[]; contradicts: string[] }>
  conclusion: { state: 'confirmed' | 'insufficient_evidence' | 'unknown'; title: string; confidence: number; rootCause: string }
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
  return {
    runId: snapshot.run_id,
    scope: { tenantId: snapshot.tenant_id ?? '', clusterId: snapshot.primary_cluster_id ?? '', resourceId: snapshot.target_resource_id ?? 'investigation', environment: snapshot.environment ?? 'unknown', namespace: snapshot.namespace ?? '', timeRange: snapshot.query_window_start && snapshot.query_window_end ? { mode: 'absolute', start: snapshot.query_window_start, end: snapshot.query_window_end } : undefined },
    intent: snapshot.intent ?? '—', status: snapshot.status ?? 'created', evidence, hypotheses,
    conclusion: { state: insufficient ? 'insufficient_evidence' : 'confirmed', title: insufficient ? '证据不足，尚不能确认根因' : rootCause, confidence, rootCause },
    ...(snapshot.action ? { action: { status: snapshot.action.status ?? 'unknown', risk: snapshot.action.risk ?? 'unknown', execution: snapshot.action.execution, verification: snapshot.action.verification } } : {}),
  }
}
