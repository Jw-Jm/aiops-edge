import { describe, expect, it } from 'vitest'
import source from './App.tsx?raw'

describe('production shell', () => {
  it('does not expose a demo environment banner', () => {
    expect(source).not.toContain('演示环境')
  })

  it('exposes a stable semantic locator for each notification entry', () => {
    expect(source).toContain('data-testid="notification-alert-item"')
  })

  it('keeps the seven product entry points stable', () => {
    for (const path of ['/overview', '/investigation', '/resources', '/observe', '/actions', '/reports', '/admin']) {
      expect(source).toContain(`path: '${path}'`)
    }
  })

  it('uses a query-preserving compatibility redirect for legacy routes', () => {
    expect(source).toContain('function LegacyRedirect')
    expect(source).toContain('new URLSearchParams(location.search)')
  })
})
