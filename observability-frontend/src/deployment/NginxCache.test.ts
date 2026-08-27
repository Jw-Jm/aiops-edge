import { describe, expect, it } from 'vitest'
import nginxConfig from '../../nginx.conf?raw'

describe('SPA entry cache policy', () => {
  it('does not cache the entry document used to discover hashed assets', () => {
    expect(nginxConfig).toMatch(/location = \/index\.html \{[\s\S]*?Cache-Control "no-store, no-cache, must-revalidate, proxy-revalidate"/)
    expect(nginxConfig).toMatch(/location = \/ \{[\s\S]*?Cache-Control "no-store, no-cache, must-revalidate, proxy-revalidate"/)
  })
})
