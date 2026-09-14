import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import KnowledgeIndexOperations from './KnowledgeIndexOperations'
import { getKnowledgeIndexStatus, reindexKnowledge } from '../../api/knowledge'

vi.mock('../../api/knowledge', () => ({ getKnowledgeIndexStatus: vi.fn(), reindexKnowledge: vi.fn() }))
let currentRole = 'admin'
vi.mock('../../store/authStore', () => ({ useAuthStore: (selector: (state: { role: string }) => unknown) => selector({ role: currentRole }) }))

describe('KnowledgeIndexOperations', () => {
  beforeEach(() => {
    currentRole = 'admin'
    vi.mocked(getKnowledgeIndexStatus).mockResolvedValue({ items: [{ knowledge_id: 'knowledge-1', version_id: 'version-2', status: 'failed', attempt: 3, last_error: 'Chroma unavailable', next_retry_at: '2026-09-10T03:00:00Z' }] })
    vi.mocked(reindexKnowledge).mockResolvedValue({ data: { queued: 1 } } as never)
  })

  it('shows version, retry state and lets only administrators reindex', async () => {
    render(<KnowledgeIndexOperations clusterId="cluster-a" />)
    expect(await screen.findByText('knowledge-1')).toBeVisible()
    expect(screen.getByText('version-2')).toBeVisible()
    expect(screen.getByText('Chroma unavailable')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: '重试索引' }))
    await waitFor(() => expect(reindexKnowledge).toHaveBeenCalledWith('cluster-a'))
  })

  it('hides the retry command for a non-administrator', async () => {
    currentRole = 'operator'
    render(<KnowledgeIndexOperations clusterId="cluster-a" />)
    await screen.findByText('knowledge-1')
    expect(screen.queryByRole('button', { name: '重试索引' })).not.toBeInTheDocument()
    currentRole = 'admin'
  })
})
