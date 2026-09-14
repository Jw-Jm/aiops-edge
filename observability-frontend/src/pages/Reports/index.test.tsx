import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Reports from './index'
import { addKnowledgeCase } from '../../api/client'
import { listReports } from '../../api/reports'

vi.mock('../../api/client', () => ({
  addKnowledgeCase: vi.fn(),
}))
vi.mock('../../api/reports', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/reports')>()
  return { ...actual, listReports: vi.fn(), generateInspectionReport: vi.fn(), downloadReport: vi.fn() }
})
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: { authScope: { activeClusterId: string } }) => unknown) => selector({ authScope: { activeClusterId: 'cluster-a' } }),
}))

describe('Reports resource event projection', () => {
  beforeEach(() => {
    vi.mocked(listReports).mockResolvedValue([{
      id: 1,
      taskId: 'report-1',
      reportType: 'ai:rca',
      verdict: 'partial',
      riskScore: 0.4,
      summary: '硬件温度异常',
      content: '# 报告正文',
      serviceName: '',
      createdAt: '2026-09-09T02:00:00Z',
      resourceUid: 'physical:edge-01',
      resourceType: 'physical_server',
      resourceName: 'edge-01',
      sourceRunId: 'report-1',
    }])
    vi.mocked(addKnowledgeCase).mockResolvedValue({ data: { inserted: true, case_id: 'case-1' } } as never)
  })

  it('只保留巡检报告与 AI 运维报告两类主入口', async () => {
    render(<MemoryRouter><Reports /></MemoryRouter>)
    // 两类主入口同时出现在分段控件中；标题与入口都可能重复渲染。
    expect((await screen.findAllByText('巡检报告')).length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('AI 运维报告').length).toBeGreaterThanOrEqual(1)
    // 不得出现第三类一级入口
    for (const forbidden of ['问题报告', '资源报告', '调查报告', '处置报告']) {
      expect(screen.queryByText(forbidden)).not.toBeInTheDocument()
    }
  })

  it('uses the typed resource as the report subject instead of calling every target a service', async () => {
    render(<MemoryRouter><Reports /></MemoryRouter>)
    await userEvent.click(await screen.findByText('AI 运维报告'))
    expect((await screen.findAllByText(/物理服务器.*edge-01/)).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/服务：edge-01/)).not.toBeInTheDocument()
  })

  it('submits a report as pending-review knowledge with its source Run', async () => {
    render(<MemoryRouter><Reports /></MemoryRouter>)
    await userEvent.click(await screen.findByText('AI 运维报告'))
    await userEvent.click(await screen.findByText('查看'))
    const submit = await screen.findByText('加入运维知识（待审核）')
    await userEvent.click(submit)
    await waitFor(() => expect(addKnowledgeCase).toHaveBeenCalledWith(expect.objectContaining({ status: 'pending_review', sourceRunId: 'report-1' })))
  })
})
