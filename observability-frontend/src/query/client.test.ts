import { describe, expect, it, vi } from 'vitest'
import { queryClient, resetScopeQueries } from './client'

describe('scope query lifecycle', () => {
  it('cancels and removes all cached queries before a scope projection is committed', async () => {
    const cancel = vi.spyOn(queryClient, 'cancelQueries').mockResolvedValue()
    const remove = vi.spyOn(queryClient, 'removeQueries').mockImplementation(() => undefined)

    await resetScopeQueries()

    expect(cancel).toHaveBeenCalledTimes(1)
    expect(remove).toHaveBeenCalledTimes(1)
    cancel.mockRestore()
    remove.mockRestore()
  })
})
