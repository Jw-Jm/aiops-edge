import { beforeEach, describe, expect, it, vi } from 'vitest'
import api from './client'
import { createK8sActionProposal, executeAiAction, type K8sActionProposalRequest } from './k8s'

vi.mock('./client', () => ({
  default: { post: vi.fn(), get: vi.fn() },
}))

describe('canonical k8s action api', () => {
  beforeEach(() => vi.clearAllMocks())

  it('creates a proposal at the canonical action boundary', async () => {
    const request: K8sActionProposalRequest = {
      idempotency_key: 'retry-1', cluster_id: '3f3c3b3a-0000-4000-8000-000000000001',
      resource_type: 'deployment', namespace: 'prod', target_name: 'orders',
      operation: 'scale', params: { replicas: 2 },
    }
    vi.mocked(api.post).mockResolvedValue({ data: { action_id: 'action-1', status: 'proposed' } } as never)

    await createK8sActionProposal(request)

    expect(api.post).toHaveBeenCalledWith('/ai/actions', request)
    expect(api.post).not.toHaveBeenCalledWith('/ops/k8s/preflight', expect.anything())
  })

  it('executes by canonical action id only', async () => {
    vi.mocked(api.post).mockResolvedValue({ data: { action_id: 'action-1', status: 'rejected' } } as never)

    await executeAiAction('action-1')

    expect(api.post).toHaveBeenCalledWith('/ai/actions/action-1/execute')
    expect(api.post).not.toHaveBeenCalledWith('/ops/k8s/execute', expect.anything())
  })
})
