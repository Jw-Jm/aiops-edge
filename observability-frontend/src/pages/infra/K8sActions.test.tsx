import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import K8sActions from './K8sActions'
import {
  createK8sActionProposal, executeAiAction,
  listK8sDeployments, listK8sNamespaces, listK8sNodes, listK8sPods,
} from '../../api/k8s'

vi.mock('../../api/k8s', () => ({
  K8S_ACTION_KINDS: {
    rollout_restart: ['deployment', 'statefulset', 'daemonset'],
    scale: ['deployment', 'statefulset'],
    delete_pod: ['pod'], evict_pod: ['pod'],
    cordon: ['node'], uncordon: ['node'], drain: ['node'],
  },
  createK8sActionProposal: vi.fn(),
  executeAiAction: vi.fn(),
  listK8sNamespaces: vi.fn(), listK8sPods: vi.fn(),
  listK8sDeployments: vi.fn(), listK8sNodes: vi.fn(),
}))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string }) => unknown) => selector({ currentClusterId: '3f3c3b3a-0000-4000-8000-000000000001' }),
}))

describe('K8sActions canonical workflow', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listK8sNamespaces).mockResolvedValue({ data: { namespaces: [] } } as never)
    vi.mocked(listK8sPods).mockResolvedValue({ data: { pods: [] } } as never)
    vi.mocked(listK8sDeployments).mockResolvedValue({ data: { deployments: [] } } as never)
    vi.mocked(listK8sNodes).mockResolvedValue({ data: { nodes: [] } } as never)
    vi.mocked(createK8sActionProposal).mockResolvedValue({ data: {
      action_id: 'action-1', run_id: 'run-1', status: 'proposed', run_status: 'awaiting_approval',
      action_version: 1, action_hash: 'hash-1', target_resource_type: 'deployment',
      target_name: 'orders', target_uid: 'uid-1', resource_version: '42', namespace: 'prod',
      operation: 'rollout_restart', execution_status: 'proposed', params: {},
    } } as never)
  })

  it('submits the selected target as a canonical proposal and waits for approval', async () => {
    const user = userEvent.setup()
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><K8sActions /></MemoryRouter>)

    await user.type(screen.getByPlaceholderText('命名空间'), 'prod')
    await user.type(screen.getByPlaceholderText('资源名称'), 'orders')
    await user.click(screen.getByRole('button', { name: '① 预检并提交审批' }))

    await waitFor(() => expect(createK8sActionProposal).toHaveBeenCalledWith(expect.objectContaining({
      idempotency_key: expect.any(String),
      cluster_id: '3f3c3b3a-0000-4000-8000-000000000001',
      resource_type: 'deployment', namespace: 'prod', target_name: 'orders',
      operation: 'rollout_restart', params: {},
    })))
    expect(await screen.findByText('Action 已创建 · 待审批')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /② 执行/ })).toBeDisabled()
    expect(executeAiAction).not.toHaveBeenCalled()
  })
})
