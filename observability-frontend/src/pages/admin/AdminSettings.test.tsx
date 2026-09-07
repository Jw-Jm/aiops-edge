import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import AdminSettings from './AdminSettings'
import { getLLMAdminConfig, saveLLMSettings, testLLMConnection, listClusters, createCluster } from '../../api/client'

vi.mock('../../api/client', () => ({
  default: { get: vi.fn() },
  getLLMAdminConfig: vi.fn(),
  saveLLMSettings: vi.fn(),
  testLLMConnection: vi.fn(),
  listLLMModels: vi.fn(),
  listClusters: vi.fn(),
  createCluster: vi.fn(),
  deleteCluster: vi.fn(),
  syncClusters: vi.fn(),
  listClusterNodes: vi.fn(),
  getClusterNamespaces: vi.fn(),
  getClusterEvents: vi.fn(),
  listAuditLogs: vi.fn(),
  listUsers: vi.fn(),
  getSystemComponents: vi.fn(),
}))

describe('AdminSettings LLM configuration', () => {
  beforeEach(() => {
    vi.mocked(getLLMAdminConfig).mockResolvedValue({
      data: {
        provider: 'deepseek',
        model: 'deepseek-chat',
        configured: true,
        proxy_ready: true,
      },
    } as never)
    vi.mocked(testLLMConnection).mockResolvedValue({ data: { success: true } } as never)
  })

  it('tests the saved configuration without sending the masked key as a credential', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><AdminSettings /></MemoryRouter>)

    await waitFor(() => expect(screen.getByText('deepseek-chat')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '测试当前配置' }))

    await waitFor(() => expect(testLLMConnection).toHaveBeenCalledTimes(2))
    expect(testLLMConnection).toHaveBeenLastCalledWith({
      provider: 'deepseek',
      model: 'deepseek-chat',
    })
  })

  it('does not save or report success when the connection endpoint returns success false', async () => {
    vi.mocked(testLLMConnection).mockResolvedValue({ data: { success: false, message: 'API key invalid' } } as never)
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><AdminSettings /></MemoryRouter>)

    await waitFor(() => expect(screen.getByText('deepseek-chat')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '测试当前配置' }))

    await waitFor(() => expect(testLLMConnection).toHaveBeenCalledTimes(2))
    expect(saveLLMSettings).not.toHaveBeenCalled()
    expect(screen.getByText('API key invalid')).toBeInTheDocument()
  })

  it('registers a managed cluster by credential_ref without accepting kubeconfig text', async () => {
    vi.mocked(listClusters).mockResolvedValue({ data: { clusters: [] } } as never)
    vi.mocked(createCluster).mockResolvedValue({ data: { cluster_id: '11111111-1111-4111-8111-111111111111' } } as never)
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><AdminSettings /></MemoryRouter>)

    fireEvent.click(screen.getByRole('tab', { name: '纳管集群' }))
    fireEvent.click(await screen.findByRole('button', { name: '+ 纳管集群' }))
    fireEvent.change(screen.getByLabelText('集群名称'), { target: { value: 'kind-aiops-kind-02' } })
    fireEvent.change(screen.getByLabelText('集群标识'), { target: { value: 'kind-aiops-kind-02' } })
    fireEvent.change(screen.getByLabelText('凭据引用'), { target: { value: 'k8s-secret://observability/aiops-managed-aiops-kind-02-kubeconfig' } })
    fireEvent.click(screen.getByRole('button', { name: /添.*加/ }))

    await waitFor(() => expect(createCluster).toHaveBeenCalledWith(expect.objectContaining({
      slug: 'kind-aiops-kind-02',
      credential_ref: 'k8s-secret://observability/aiops-managed-aiops-kind-02-kubeconfig',
    })))
    expect(vi.mocked(createCluster).mock.calls[0][0]).not.toHaveProperty('kubeconfig')
  })
})
