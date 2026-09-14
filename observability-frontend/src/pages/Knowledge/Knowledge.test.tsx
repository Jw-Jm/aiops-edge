import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Knowledge from './index'
import * as knowledgeApi from '../../api/knowledge'
import { useScopeStore } from '../../store/scopeStore'

vi.mock('../../api/knowledge', async () => {
  const actual = await vi.importActual<typeof import('../../api/knowledge')>('../../api/knowledge')
  return {
    ...actual,
    listKnowledge: vi.fn(),
    getKnowledge: vi.fn(),
    getKnowledgeIndexStatus: vi.fn(),
    createKnowledge: vi.fn(),
    submitKnowledge: vi.fn(),
    searchKnowledge: vi.fn(),
    reviewKnowledge: vi.fn(),
    disableKnowledge: vi.fn(),
  }
})

describe('Knowledge workspace', () => {
  beforeEach(() => {
    useScopeStore.setState({ capabilities: ['knowledge.write'] })
    vi.mocked(knowledgeApi.listKnowledge).mockResolvedValue({
      items: [{ knowledge_id: 'k-1', title: 'Pod 未就绪处置', summary: '检查 Pod 和节点', knowledge_type: 'incident', status: 'published', scope_type: 'cluster', cluster_id: 'cluster-a', current_version_id: 'v-1' }],
      meta: { index_available: false },
    })
    vi.mocked(knowledgeApi.getKnowledgeIndexStatus).mockResolvedValue({
      items: [],
      meta: { index_available: false, error: '索引服务暂不可用' },
    })
    vi.mocked(knowledgeApi.createKnowledge).mockResolvedValue({ data: { knowledge_id: 'k-2' } } as never)
    vi.mocked(knowledgeApi.submitKnowledge).mockResolvedValue({ data: { status: 'pending_review' } } as never)
    vi.mocked(knowledgeApi.getKnowledge).mockImplementation(async (_clusterId, knowledgeId) => knowledgeId === 'k-2'
      ? { data: { knowledge_id: 'k-2', title: '新草稿', summary: '', knowledge_type: 'incident', status: 'draft', scope_type: 'cluster', cluster_id: 'cluster-a', draft_version_id: 'v-2' }, version: { version_id: 'v-2', content: '', source_kind: 'manual' } }
      : { data: { knowledge_id: 'k-1', title: 'Pod 未就绪处置', summary: '检查 Pod 和节点', knowledge_type: 'incident', status: 'published', scope_type: 'cluster', cluster_id: 'cluster-a', current_version_id: 'v-1' }, version: { version_id: 'v-1', content: '检查节点和容器运行态', source_kind: 'manual' } })
    vi.mocked(knowledgeApi.searchKnowledge).mockRejectedValue(new Error('unavailable'))
    vi.mocked(knowledgeApi.reviewKnowledge).mockResolvedValue({ data: { status: 'published' } } as never)
    vi.mocked(knowledgeApi.disableKnowledge).mockResolvedValue({ data: { status: 'disabled' } } as never)
  })

  it('待审阅知识必须经人工批准后发布，拒绝必须留痕（§6.6）', async () => {
    vi.mocked(knowledgeApi.listKnowledge).mockResolvedValue({
      items: [{ knowledge_id: 'k-p', title: 'AI 沉淀草稿', summary: '由运维任务生成', knowledge_type: 'incident', status: 'pending_review', scope_type: 'cluster', cluster_id: 'cluster-a', current_version_id: 'v-p' }],
      meta: { index_available: false },
    })
    vi.mocked(knowledgeApi.getKnowledge).mockResolvedValue({
      data: { knowledge_id: 'k-p', title: 'AI 沉淀草稿', summary: '由运维任务生成', knowledge_type: 'incident', status: 'pending_review', scope_type: 'cluster', cluster_id: 'cluster-a' },
      version: { version_id: 'v-p', content: '第一段结论\n\n第二段证据', source_kind: 'ai_operation' },
    })
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge']}><Routes><Route path="/clusters/:clusterId/knowledge" element={<Knowledge />} /></Routes></MemoryRouter>)

    const reviewers = await screen.findAllByTestId('knowledge-review')
    expect(reviewers.length).toBeGreaterThanOrEqual(1)
    const approveButtons = screen.getAllByRole('button', { name: '批准并发布' })
    await userEvent.click(approveButtons[0])
    await waitFor(() => expect(knowledgeApi.reviewKnowledge).toHaveBeenCalledWith('cluster-a', 'k-p', 'approve', ''))
  })

  it('禁用后必须验证检索不再命中；仍命中时验证不得通过（§6.6 删除验证）', async () => {
    vi.mocked(knowledgeApi.listKnowledge).mockResolvedValue({
      items: [{ knowledge_id: 'k-d', title: '待删除知识', summary: '关键词', knowledge_type: 'incident', status: 'disabled', scope_type: 'cluster', cluster_id: 'cluster-a' }],
      meta: { index_available: true },
    })
    vi.mocked(knowledgeApi.getKnowledge).mockResolvedValue({
      data: { knowledge_id: 'k-d', title: '待删除知识', summary: '关键词', knowledge_type: 'incident', status: 'disabled', scope_type: 'cluster', cluster_id: 'cluster-a' },
      version: { version_id: 'v-d', content: '正文', source_kind: 'manual' },
    })
    // 索引仍能命中 → 删除验证必须失败
    vi.mocked(knowledgeApi.searchKnowledge).mockResolvedValue({ items: [{ knowledge_id: 'k-d', title: '待删除知识' }], meta: { index_available: true } })
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge']}><Routes><Route path="/clusters/:clusterId/knowledge" element={<Knowledge />} /></Routes></MemoryRouter>)

    await userEvent.click(await screen.findAllByTestId('knowledge-disable').then((nodes) => nodes[0].querySelector('button') as HTMLElement))
    const verify = await screen.findByRole('button', { name: '验证检索不再命中' })
    await userEvent.click(verify)
    expect(await screen.findByText('删除验证未通过')).toBeVisible()

    // 索引已同步 → 验证通过
    vi.mocked(knowledgeApi.searchKnowledge).mockResolvedValue({ items: [], meta: { index_available: true } })
    await userEvent.click(verify)
    expect(await screen.findByText('删除验证通过')).toBeVisible()
  })

  it('正文按段落渲染并提供段落级引用定位（§6.6 引用必须可定位）', async () => {
    vi.mocked(knowledgeApi.getKnowledge).mockResolvedValue({
      data: { knowledge_id: 'k-1', title: 'Pod 未就绪处置', summary: '检查 Pod 和节点', knowledge_type: 'incident', status: 'published', scope_type: 'cluster', cluster_id: 'cluster-a', current_version_id: 'v-1' },
      version: { version_id: 'v-1', content: '第一段：现象\n\n第二段：证据\n\n第三段：处置', source_kind: 'manual' },
    })
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge']}><Routes><Route path="/clusters/:clusterId/knowledge" element={<Knowledge />} /></Routes></MemoryRouter>)

    const citations = await screen.findAllByRole('button', { name: /复制第 \d+ 段引用/ })
    expect(citations.length).toBeGreaterThanOrEqual(3)
  })

  it('shows governed tabs and keeps browsing available when semantic search is degraded', async () => {
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge']}><Routes><Route path="/clusters/:clusterId/knowledge" element={<Knowledge />} /></Routes></MemoryRouter>)

    expect(await screen.findByText('运维知识')).toBeVisible()
    expect(screen.getByText('检索与索引')).toBeVisible()
    expect(screen.getByRole('tab', { name: '故障案例' })).toBeVisible()
    expect(screen.getByRole('tab', { name: '运维文档' })).toBeVisible()
    expect(screen.getByRole('tab', { name: '内置 Playbook' })).toBeVisible()
    expect(screen.getByText('当前集群', { exact: true })).toBeVisible()
    expect(screen.getByText('语义检索暂不可用，正文仍可浏览')).toBeVisible()

    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }))
    expect(knowledgeApi.createKnowledge).toHaveBeenCalledWith('cluster-a', expect.objectContaining({ status: 'draft' }))
  })

  it('loads the selected knowledge version for the inspector', async () => {
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge']}><Routes><Route path="/clusters/:clusterId/knowledge" element={<Knowledge />} /></Routes></MemoryRouter>)

    await userEvent.click(await screen.findByTestId('knowledge-list').then((list) => list.querySelector('strong') as HTMLElement))
    const inspectors = await screen.findAllByTestId('knowledge-inspector')
    expect(inspectors.some((element) => element.textContent?.includes('检查节点和容器运行态'))).toBe(true)
    expect(knowledgeApi.getKnowledge).toHaveBeenCalledWith('cluster-a', 'k-1')
  })

  it('keeps submitting a saved draft separate from saving it', async () => {
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge']}><Routes><Route path="/clusters/:clusterId/knowledge" element={<Knowledge />} /></Routes></MemoryRouter>)

    await userEvent.click(await screen.findByRole('button', { name: '保存草稿' }))
    await userEvent.click(await screen.findByRole('button', { name: '提交审核' }))
    expect(knowledgeApi.submitKnowledge).toHaveBeenCalledWith('cluster-a', 'k-2')
  })

  it('opens a blank editor on the explicit new route', async () => {
    render(<MemoryRouter initialEntries={['/clusters/cluster-a/knowledge/new']}><Routes><Route path="/clusters/:clusterId/knowledge/new" element={<Knowledge />} /></Routes></MemoryRouter>)

    expect(await screen.findByText('选择一条知识查看正文与引用定位')).toBeVisible()
  })
})
