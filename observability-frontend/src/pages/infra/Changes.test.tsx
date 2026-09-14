import { render, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Changes from './Changes'
import { getChanges, postChange } from '../../api/client'

vi.mock('../../api/client', () => ({ getChanges: vi.fn(), postChange: vi.fn() }))
vi.mock('../../store/scopeStore', () => ({
  useScopeStore: (selector: (state: { authScope: { activeClusterId: string } | null; clusters: never[] }) => unknown) =>
    selector({ authScope: { activeClusterId: 'cluster-1' }, clusters: [] }),
}))

describe('Changes server-side pagination and filtering', () => {
  beforeEach(() => {
    vi.mocked(getChanges).mockResolvedValue({ data: { changes: [], total: 0 } } as never)
    vi.mocked(postChange).mockResolvedValue({ data: { ok: true } } as never)
  })

  it('requests a page with server-side filter parameters instead of a fixed 200-row snapshot', async () => {
    render(<Changes />)
    await waitFor(() => expect(getChanges).toHaveBeenCalledWith({
      cluster_id: 'cluster-1', page: 1, page_size: 20, service: '', change_type: '',
    }))
  })
})
