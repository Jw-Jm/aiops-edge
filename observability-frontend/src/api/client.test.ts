import { describe, expect, it } from 'vitest'
import api from './client'

describe('server-owned request context', () => {
  it('does not inject a fixed tenant header into browser requests', () => {
    const tenantHeader = ['X', 'Tenant-ID'].join('-')
    expect((api.defaults.headers.common as Record<string, unknown>)[tenantHeader]).toBeUndefined()
    expect(api.defaults.withCredentials).toBe(true)
  })
})
