import React from 'react'
import { Alert, Button, Tag } from 'antd'
import type { AssistantAnswer, AssistantConversationScope, EvidenceCitation, KnowledgeCitation } from '../../api/assistant'

export interface InvestigationDraftRequest extends AssistantConversationScope {
  status: 'draft'
}

function EvidenceItem({ citation, onScopeClick }: { citation: EvidenceCitation; onScopeClick?: () => void }) {
  return <li className="assistant-answer-card__citation">
    <strong>{citation.label}</strong>
    <span>{citation.summary}</span>
    <small>{citation.source || '平台观测'} · {citation.observedAt || '时间未知'}{citation.resourceUid ? ` · ${citation.resourceUid}` : ''}</small>
    {onScopeClick && <button type="button" className="link-button" onClick={onScopeClick}>回到此 Scope</button>}
  </li>
}

function KnowledgeItem({ citation, onScopeClick }: { citation: KnowledgeCitation; onScopeClick?: () => void }) {
  return <li className="assistant-answer-card__citation">
    <strong>{citation.title}</strong>
    <span>{citation.excerpt || '已通过已发布版本校验'}</span>
    <small>{citation.scopeType === 'cluster' ? `当前集群${citation.clusterId ? ` · ${citation.clusterId}` : ''}` : '平台通用'} · {citation.versionId}</small>
    {onScopeClick && <button type="button" className="link-button" onClick={onScopeClick}>回到此 Scope</button>}
  </li>
}

export default function AssistantAnswerCard({ answer, scope, onCreateInvestigationDraft, onCitationClick }: {
  answer: AssistantAnswer
  scope: AssistantConversationScope
  onCreateInvestigationDraft?: (request: InvestigationDraftRequest) => void
  onCitationClick?: (scope: AssistantConversationScope) => void
}) {
  const canDraft = answer.capabilities.createInvestigationDraft && Boolean(onCreateInvestigationDraft)
  return <article className="assistant-answer-card" data-testid="assistant-answer-card">
    <div className="assistant-answer-card__meta">
      <Tag color={answer.completeness === 'complete' ? 'green' : 'gold'}>{answer.completeness === 'complete' ? '证据完整' : '部分证据'}</Tag>
      <span>生成于 {answer.generatedAt}</span>
    </div>
    <section>
      <h2>结论</h2>
      <p className="assistant-answer-card__conclusion">{answer.conclusion}</p>
    </section>
    <section>
      <h3>关键事实证据</h3>
      {answer.evidenceCitations.length ? <ul>{answer.evidenceCitations.map((citation) => <EvidenceItem key={citation.id} citation={citation} onScopeClick={onCitationClick ? () => onCitationClick(scope) : undefined} />)}</ul> : <Alert type="info" showIcon message="当前回答没有可回溯的事实证据" />}
    </section>
    <section>
      <h3>运维知识引用</h3>
      {answer.knowledgeCitations.length ? <ul>{answer.knowledgeCitations.map((citation) => <KnowledgeItem key={`${citation.knowledgeId}:${citation.versionId}`} citation={citation} onScopeClick={onCitationClick ? () => onCitationClick(scope) : undefined} />)}</ul> : <Alert type="info" showIcon message="当前回答没有引用已发布运维知识" />}
    </section>
    <section>
      <h3>不确定性与缺失证据</h3>
      {answer.limitations.length ? <ul>{answer.limitations.map((item) => <li key={item}>{item}</li>)}</ul> : <p>未发现额外限制。</p>}
    </section>
    <section>
      <h3>建议下一步</h3>
      {answer.recommendedNextSteps.length ? <div className="assistant-answer-card__actions">{answer.recommendedNextSteps.map((step) => <div key={step.id} className="assistant-answer-card__next-step">
        <span>{step.label}</span>
        {step.action === 'investigation_draft' && canDraft && <Button size="small" type="primary" onClick={() => onCreateInvestigationDraft?.({ ...scope, status: 'draft' })}>创建调查草稿</Button>}
      </div>)}</div> : <p>暂无建议。</p>}
    </section>
    <footer className="assistant-answer-card__footer">
      {answer.sourceFreshness.map((source) => <span key={source.source}>{source.source}: {source.freshness === 'fresh' ? '新鲜' : source.freshness === 'stale' ? '陈旧' : '未知'}</span>)}
      <span>仅可提出建议，不在对话中直接执行动作</span>
    </footer>
  </article>
}
