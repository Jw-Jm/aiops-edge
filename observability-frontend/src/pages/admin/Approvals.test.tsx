import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Approvals from './Approvals'
import { decideAction, listActions } from '../../api/client'

vi.mock('../../store/authStore', () => ({
  useAuthStore: (selector: (state: { role: string }) => unknown) => selector({ role: 'admin' }),
}))

vi.mock('../../api/client', async () => {
  return { listActions: vi.fn(), decideAction: vi.fn() }
})

const action = {
  action_id: 'action-1', run_id: 'run-1', action_type: 'kubernetes',
  action_hash: 'abc123', hash_schema_version: 2, action_version: 7,
  policy_version: 'action-policy-v1', preflight_status: 'passed',
  target_resource_type: 'deployment', status: 'proposed' as const, dry_run: true,
  target_name: 'orders', target_uid: 'uid-123', resource_version: 'rv-42', namespace: 'prod',
  operation: 'scale', execution_status: 'proposed', params: { replicas: 2 },
}

describe('canonical approval center', () => {
  beforeEach(() => {
    vi.mocked(listActions).mockResolvedValue({ data: { actions: [action], count: 1 } } as never)
    vi.mocked(decideAction).mockResolvedValue({ data: { action_id: action.action_id } } as never)
  })

  it('renders the immutable target identity, hash and canonical parameters', async () => {
    const user = userEvent.setup()
    render(<Approvals />)

    await user.click(await screen.findByText('orders'))

    expect(screen.getByTestId('canonical-action-fields')).toHaveTextContent('uid-123')
    expect(screen.getByTestId('canonical-action-fields')).toHaveTextContent('rv-42')
    expect(screen.getByTestId('canonical-action-fields')).toHaveTextContent('abc123')
    expect(screen.getByTestId('canonical-action-fields')).toHaveTextContent('7')
    expect(screen.getByTestId('canonical-action-params')).toHaveTextContent('"replicas": 2')
  })

  it('sends a server-derived decision without client approver or hash', async () => {
    const user = userEvent.setup()
    render(<Approvals />)

    await user.click((await screen.findAllByRole('button', { name: /批\s*准/ }))[0])
    await user.click(screen.getByRole('button', { name: '确认批准执行' }))

    await waitFor(() => expect(decideAction).toHaveBeenCalledWith('action-1', {
      decision: 'approved', action_version: 7, idempotency_key: 'ui-approve-action-1-7',
    }))
    const body = vi.mocked(decideAction).mock.calls[0][1]
    expect(body).not.toHaveProperty('approver')
    expect(body).not.toHaveProperty('action_hash')
  })

  it('surfaces stale-version rejection from the API', async () => {
    vi.mocked(decideAction).mockRejectedValueOnce({ response: { status: 409, data: { error: 'stale_action' } } })
    const user = userEvent.setup()
    render(<Approvals />)

    await user.click((await screen.findAllByRole('button', { name: /批\s*准/ }))[0])
    await user.click(screen.getByRole('button', { name: '确认批准执行' }))

    expect(await screen.findByText('stale_action')).toBeInTheDocument()
  })
})
