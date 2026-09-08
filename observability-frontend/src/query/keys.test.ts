import { describe, expect, it } from 'vitest'
import { queryKeys } from './keys'

describe('scope-isolated query keys', () => {
  it('includes tenant and active cluster in every cluster-scoped key', () => {
    expect(queryKeys.services({ tenantId: 'tenant-a', activeClusterId: 'cluster-a' })).toEqual([
      'services', 'tenant-a', 'cluster-a', '-', '-', '-', '-',
    ])
  })
})
