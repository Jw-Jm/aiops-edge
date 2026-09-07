import { describe, expect, it } from 'vitest'
import api, { getCapacityForecast } from './client'
import source from './client.ts?raw'

describe('server-owned request context', () => {
  it('does not inject a fixed tenant header into browser requests', () => {
    const tenantHeader = ['X', 'Tenant-ID'].join('-')
    expect((api.defaults.headers.common as Record<string, unknown>)[tenantHeader]).toBeUndefined()
    expect(api.defaults.withCredentials).toBe(true)
  })

  it('uses canonical approvals and never exposes retired approval routes', () => {
    expect(source).toContain("'/ai/actions'")
    expect(source).not.toContain("'/ops/tasks'")
  })

  it('uses the supported cpu capacity metric', async () => {
    const originalGet = api.get
    const calls: unknown[] = []
    api.get = (async (...args: unknown[]) => { calls.push(args); return { data: {} } }) as typeof api.get
    await getCapacityForecast({ metric: 'cpu' })
    api.get = originalGet
    expect(calls).toEqual([['/capacity/forecast', { params: { metric: 'cpu' } }]])
  })
})
