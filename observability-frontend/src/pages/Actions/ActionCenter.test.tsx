import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import ActionCenter from './ActionCenter'
import { listActions } from '../../api/client'

vi.mock('../../api/client', () => ({
  listActions: vi.fn(),
  getAction: vi.fn(),
  decideAction: vi.fn(),
}))
vi.mock('../../store/authStore', () => ({ useAuthStore: (selector: (state: { role: string }) => unknown) => selector({ role: 'approver' }) }))
const scopeState: { authScope: { activeClusterId: string }; capabilities: string[] } = { authScope: { activeClusterId: 'cluster-a' }, capabilities: [] }
vi.mock('../../store/scopeStore', () => ({ useScopeStore: (selector: (state: typeof scopeState) => unknown) => selector(scopeState) }))

const action = {
  action_id: 'action-1', run_id: 'run-1', cluster_id: 'cluster-a', action_type: 'restart_workload', action_hash: 'hash', hash_schema_version: 2, action_version: 1,
  preflight_status: 'passed', target_resource_type: 'deployment', status: 'proposed', dry_run: false, target_name: 'payment', target_uid: 'deployment/payment', resource_version: '7', namespace: 'payment', operation: '重启工作负载', execution_status: 'not_started',
  idempotency_key: 'idem-1',
}

describe('ActionCenter capability gate', () => {
  beforeEach(() => {
    scopeState.capabilities = []
    vi.mocked(listActions).mockResolvedValue({ data: { actions: [action], count: 1 } } as never)
  })

  it('requests and displays only actions for the active cluster', async () => {
    render(<MemoryRouter><ActionCenter /></MemoryRouter>)
    await screen.findByText('重启工作负载')
    expect(vi.mocked(listActions)).toHaveBeenCalledWith(expect.objectContaining({ cluster_id: 'cluster-a' }))
  })

  it('keeps mutable actions read-only when the session capability is absent', async () => {
    render(<MemoryRouter><ActionCenter /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: '查看详情' }))
    expect(await screen.findByText('当前能力只读：等待服务端 capability 授权')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '批准执行' })).not.toBeInTheDocument()
  })

  it('shows approval controls only after the returned capability is present', async () => {
    scopeState.capabilities = ['kubernetes.workload.write']
    render(<MemoryRouter><ActionCenter /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: '查看详情' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '批准执行' })).toBeInTheDocument())
  })

  it('shows resource version and an explicit execution phase without green health semantics', async () => {
    vi.mocked(listActions).mockResolvedValueOnce({ data: { actions: [{ ...action, status: 'approved', execution_status: 'running' }] } } as never)
    render(<MemoryRouter><ActionCenter /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('tab', { name: /待执行/ }))
    fireEvent.click(await screen.findByRole('button', { name: '查看详情' }))
    expect(await screen.findByText('ResourceVersion')).toBeInTheDocument()
    expect(screen.getByText('idem-1')).toBeInTheDocument()
    expect(screen.getByText('执行中')).toHaveClass('flow')
    expect(screen.getByText('执行中')).not.toHaveClass('status--ok')
  })
})
