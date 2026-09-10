export interface AssistantConversationScope {
  clusterId: string
  resourceUid?: string
  from: string
  to: string
  knowledgeScope: 'platform_common_and_current_cluster'
}

export interface EvidenceCitation {
  id: string
  label: string
  resourceUid?: string
  observedAt?: string
  summary: string
  source?: string
}

export interface KnowledgeCitation {
  knowledgeId: string
  title: string
  versionId: string
  scopeType: 'platform_common' | 'cluster'
  clusterId?: string
  status: 'published'
  excerpt?: string
}

export interface RecommendedStep {
  id: string
  label: string
  action: 'investigation_draft' | 'observe' | 'review'
  resourceUid?: string
}

export interface SourceFreshness {
  source: string
  observedAt?: string
  freshness: 'fresh' | 'stale' | 'unknown'
}

export interface AssistantAnswer {
  conclusion: string
  evidenceCitations: EvidenceCitation[]
  knowledgeCitations: KnowledgeCitation[]
  limitations: string[]
  recommendedNextSteps: RecommendedStep[]
  capabilities: { createInvestigationDraft: boolean; proposeAction: boolean; executeAction: false }
  generatedAt: string
  sourceFreshness: SourceFreshness[]
  completeness: 'complete' | 'partial'
}

export interface InvestigationDraftInput {
  clusterId: string
  resourceUid?: string
  scope: AssistantConversationScope
  status: 'draft'
  symptom?: string
}

export const createInvestigationDraft = async (input: InvestigationDraftInput) => {
  // A draft is a client-side handoff to the explicit Investigation form. It
  // must not create an AI Run or action before the operator submits that form.
  return { data: input, status: input.status as 'draft' }
}

export function parseAssistantAnswer(value: unknown): AssistantAnswer | null {
  if (!value || typeof value !== 'object') return null
  const raw = value as Record<string, unknown>
  const answer = (raw.answer && typeof raw.answer === 'object' ? raw.answer : raw) as Record<string, unknown>
  if (typeof answer.conclusion !== 'string') return null
  const capabilities = (answer.capabilities && typeof answer.capabilities === 'object' ? answer.capabilities : {}) as Record<string, unknown>
  return {
    conclusion: answer.conclusion,
    evidenceCitations: Array.isArray(answer.evidence_citations) ? answer.evidence_citations as EvidenceCitation[] : [],
    knowledgeCitations: Array.isArray(answer.knowledge_citations) ? answer.knowledge_citations as KnowledgeCitation[] : [],
    limitations: Array.isArray(answer.limitations) ? answer.limitations.filter((item): item is string => typeof item === 'string') : [],
    recommendedNextSteps: Array.isArray(answer.recommended_next_steps) ? answer.recommended_next_steps as RecommendedStep[] : [],
    capabilities: {
      createInvestigationDraft: capabilities.create_investigation_draft !== false,
      proposeAction: capabilities.propose_action === true,
      executeAction: false,
    },
    generatedAt: typeof answer.generated_at === 'string' ? answer.generated_at : new Date().toISOString(),
    sourceFreshness: Array.isArray(answer.source_freshness) ? answer.source_freshness as SourceFreshness[] : [],
    completeness: answer.completeness === 'complete' ? 'complete' : 'partial',
  }
}
