import { describe, expect, it } from 'vitest'
import { TENANT_ID } from './client'

describe('tenant request context', () => {
  it('uses a canonical UUID instead of the legacy default tenant alias', () => {
    expect(TENANT_ID).toBe('7ed01afc-cc79-4ecd-8767-a2befa6168ad')
  })
})
