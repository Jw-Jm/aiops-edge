import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import AdminSettings from './AdminSettings'
import { getLLMAdminConfig, saveLLMSettings, testLLMConnection } from '../../api/client'

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
        base_url: 'https://api.deepseek.com/v1',
        configured: true,
        api_key_set: true,
        api_key_masked: 'sk-***',
      },
    } as never)
    vi.mocked(testLLMConnection).mockResolvedValue({ data: { success: true } } as never)
  })

  it('tests the saved configuration without sending the masked key as a credential', async () => {
    render(<MemoryRouter><AdminSettings /></MemoryRouter>)

    await waitFor(() => expect(screen.getByText('deepseek-chat')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '测试当前配置' }))

    await waitFor(() => expect(testLLMConnection).toHaveBeenCalledTimes(2))
    expect(testLLMConnection).toHaveBeenLastCalledWith({
      provider: 'deepseek',
      base_url: 'https://api.deepseek.com/v1',
      model: 'deepseek-chat',
    })
  })

  it('does not save or report success when the connection endpoint returns success false', async () => {
    vi.mocked(testLLMConnection).mockResolvedValue({ data: { success: false, message: 'API key invalid' } } as never)
    render(<MemoryRouter><AdminSettings /></MemoryRouter>)

    await waitFor(() => expect(screen.getByText('deepseek-chat')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '测试当前配置' }))

    await waitFor(() => expect(testLLMConnection).toHaveBeenCalledTimes(2))
    expect(saveLLMSettings).not.toHaveBeenCalled()
    expect(screen.getByText('API key invalid')).toBeInTheDocument()
  })
})
