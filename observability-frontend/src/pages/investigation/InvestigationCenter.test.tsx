import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import InvestigationCenter, { projectInvestigationSource } from './InvestigationCenter'
import { listRuns } from '../../api/client'
import source from './InvestigationCenter.tsx?raw'

vi.mock('../../api/client', () => ({ listRuns: vi.fn() }))
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: { authScope: { activeClusterId: string } | null }) => unknown) => selector({ authScope: { activeClusterId: 'cluster-1' } }),
}))

describe('InvestigationCenter identity projection', () => {
  beforeEach(() => {
    vi.mocked(listRuns).mockResolvedValue({ data: { runs: [{
      run_id: 'run-1', request_id: 'request-1', tenant_id: 'tenant-1',
      primary_cluster_id: 'cluster-1', target_resource_id: 'checkout', intent: 'investigate',
      target_type: 'service', action_mode: 'read_only',
      status: 'created', principal_id: 'user-123', created_by: 'user-123', principal_type: 'user',
      created_at: '2026-08-26T00:00:00Z',
    }, {
      run_id: 'run-other', request_id: 'request-other', tenant_id: 'tenant-1',
      primary_cluster_id: 'cluster-2', target_resource_id: 'other', intent: 'other',
      target_type: 'pod', status: 'created', principal_id: 'user-456', created_by: 'user-456',
      created_at: '2026-08-26T00:00:00Z',
    }] } } as never)
  })

  it('keeps the first screen to the fixed summary columns and moves full identity into the drawer', async () => {
    const user = userEvent.setup()
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><InvestigationCenter /></MemoryRouter>)
    // 首屏固定列：资源、症状、状态、操作 常驻；证据/发起时间在 <xl 视口按设计收起。
    await screen.findByRole('button', { name: '查看详情' })
    for (const header of ['资源', '症状', '状态', '操作']) {
      expect(screen.getAllByText(header).length).toBeGreaterThan(0)
    }
    for (const header of ['证据', '发起时间']) {
      expect(source).toContain(`title: '${header}'`)
    }
    // 辅助表头不再出现在首屏。
    for (const removed of ['集群', '影响', '持续时间', '冻结窗口', 'Run ID', '根因', '置信度', '发起人']) {
      expect(screen.queryByText(removed)).not.toBeInTheDocument()
    }
    expect(screen.getByText('已创建')).toBeInTheDocument()
    expect(screen.queryByText('created')).not.toBeInTheDocument()

    // 完整字段仍可从详情 Drawer 读取。
    await user.click(screen.getByRole('button', { name: '查看详情' }))
    expect(await screen.findByText('Run ID')).toBeInTheDocument()
    expect(screen.getByText('run-1')).toBeInTheDocument()
    expect(screen.getByText('集群 ID')).toBeInTheDocument()
    expect(screen.getByText('冻结窗口')).toBeInTheDocument()
    expect(screen.getByText('发起人')).toBeInTheDocument()
    expect(screen.getByText('user-123')).toBeInTheDocument()
    expect(screen.queryByText('user-456')).not.toBeInTheDocument()
    expect(screen.queryByText('system')).not.toBeInTheDocument()
  })

  it('labels the run source instead of a synthetic system identity', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><InvestigationCenter /></MemoryRouter>)
    expect(await screen.findByText('人工发起')).toBeInTheDocument()
    expect(screen.queryByText('system')).not.toBeInTheDocument()
  })

  it('shows an error state when the persisted run source is unavailable', async () => {
    vi.mocked(listRuns).mockRejectedValueOnce(new Error('run store unavailable'))
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><InvestigationCenter /></MemoryRouter>)
    expect(await screen.findByText('run store unavailable')).toBeInTheDocument()
  })

  it('projects investigation sources from the persisted principal type', () => {
    expect(projectInvestigationSource('user', 'read_only')).toBe('human')
    expect(projectInvestigationSource('system', 'read_only')).toBe('system_auto')
    expect(projectInvestigationSource('system', 'plan_only')).toBe('system_suggested')
    expect(projectInvestigationSource('', '')).toBe('unknown')
  })
})
