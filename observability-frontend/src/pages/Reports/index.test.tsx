import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import Reports from './index'
import { addKnowledgeCase, listReports } from '../../api/client'

vi.mock('../../api/client', () => ({
  listReports: vi.fn(),
  addKnowledgeCase: vi.fn(),
  default: { get: vi.fn() },
}))
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: { authScope: { activeClusterId: string } }) => unknown) => selector({ authScope: { activeClusterId: 'cluster-a' } }),
}))

describe('Reports resource event projection', () => {
  beforeEach(() => {
  vi.mocked(listReports).mockResolvedValue({ data: { reports: [{
      task_id: 'report-1', report_type: 'report', cluster_id: 'cluster-a',
      resource_uid: 'physical:edge-01', resource_type: 'physical_server', resource_name: 'edge-01',
      summary: '硬件温度异常', created_at: '2026-09-09T02:00:00Z',
  }] } } as never)
    vi.mocked(addKnowledgeCase).mockResolvedValue({ data: { inserted: true, case_id: 'case-1' } } as never)
  })

  it('uses the typed resource as the report subject instead of calling every target a service', async () => {
    render(<MemoryRouter><Reports /></MemoryRouter>)
    expect((await screen.findAllByText(/物理服务器.*edge-01/)).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/服务：edge-01/)).not.toBeInTheDocument()
  })

  it('submits a report as pending-review knowledge with its source Run', async () => {
    render(<MemoryRouter><Reports /></MemoryRouter>)
    const submit = await screen.findByText('加入运维知识（待审核）')
    await userEvent.click(submit)
    await waitFor(() => expect(addKnowledgeCase).toHaveBeenCalledWith(expect.objectContaining({ status: 'pending_review', sourceRunId: 'report-1' })))
  })
})
