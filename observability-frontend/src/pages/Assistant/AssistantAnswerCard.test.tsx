import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import AssistantAnswerCard from './AssistantAnswerCard'
import type { AssistantAnswer, AssistantConversationScope } from '../../api/assistant'

const scope: AssistantConversationScope = {
  clusterId: 'cluster-a', resourceUid: 'uid-order-api',
  from: '2026-09-10T10:00:00Z', to: '2026-09-10T11:00:00Z',
  knowledgeScope: 'platform_common_and_current_cluster',
}

const answer: AssistantAnswer = {
  conclusion: 'order-api 的 Pod 就绪率下降，证据支持镜像拉取失败。',
  evidenceCitations: [{ id: 'e-1', label: 'Pod 事件', resourceUid: 'uid-order-api', observedAt: '2026-09-10T10:55:00Z', summary: 'ImagePullBackOff' }],
  knowledgeCitations: [{ knowledgeId: 'k-1', title: '镜像拉取失败处置', versionId: 'v-1', scopeType: 'cluster', clusterId: 'cluster-a', status: 'published', excerpt: '检查镜像仓库凭据' }],
  limitations: ['尚未获取节点侧镜像仓库连通性'],
  recommendedNextSteps: [{ id: 'step-1', label: '检查节点到镜像仓库的连通性', action: 'investigation_draft' }],
  capabilities: { createInvestigationDraft: true, proposeAction: true, executeAction: false },
  generatedAt: '2026-09-10T11:00:00Z',
  sourceFreshness: [{ source: 'pod-events', observedAt: '2026-09-10T10:55:00Z', freshness: 'fresh' }],
  completeness: 'partial',
}

describe('AssistantAnswerCard', () => {
  it('renders ordered evidence-grounded sections and only offers a draft action', async () => {
    const onCreateInvestigationDraft = vi.fn()
    const onCitationClick = vi.fn()
    render(<AssistantAnswerCard answer={answer} scope={scope} onCreateInvestigationDraft={onCreateInvestigationDraft} onCitationClick={onCitationClick} />)

    expect(screen.getByRole('heading', { name: '结论' })).toBeVisible()
    expect(screen.getByText('关键事实证据')).toBeVisible()
    expect(screen.getByText('运维知识引用')).toBeVisible()
    expect(screen.getByText('不确定性与缺失证据')).toBeVisible()
    expect(screen.getByText('建议下一步')).toBeVisible()
    expect(screen.queryByRole('button', { name: '立即执行' })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '创建调查草稿' }))
    expect(onCreateInvestigationDraft).toHaveBeenCalledWith(expect.objectContaining({ clusterId: 'cluster-a', resourceUid: 'uid-order-api', status: 'draft' }))
    await userEvent.click(screen.getAllByRole('button', { name: '回到此 Scope' })[0])
    expect(onCitationClick).toHaveBeenCalledWith(scope)
  })

  it('always renders the six fixed sections in the contractual order', () => {
    render(<AssistantAnswerCard answer={answer} scope={scope} />)
    const headings = Array.from(document.querySelectorAll('.assistant-answer-card h2, .assistant-answer-card h3')).map((node) => node.textContent)
    expect(headings).toEqual(['结论', '关键事实证据', '运维知识引用', '不确定性与缺失证据', '建议下一步', '受控动作'])
    // 边界必须显式声明，且不存在直接执行入口。
    expect(screen.getByText('仅可提出建议，不在对话中直接执行动作')).toBeVisible()
    expect(screen.queryByRole('button', { name: /立即执行|执行动作/ })).not.toBeInTheDocument()
  })

  it('shows explicit gaps instead of fabricating evidence or knowledge', () => {
    render(<AssistantAnswerCard answer={{ ...answer, evidenceCitations: [], knowledgeCitations: [], limitations: [], recommendedNextSteps: [] }} scope={scope} />)
    expect(screen.getByText('当前回答没有可回溯的事实证据')).toBeVisible()
    expect(screen.getByText('当前回答没有引用已发布运维知识')).toBeVisible()
    // 缺口不能伪装成"未发现关键证据缺口"。
    expect(screen.getByText('未发现额外限制。')).toBeVisible()
    expect(screen.getByText('尚未提供建议')).toBeVisible()
  })
})
