import { describe, expect, it } from 'vitest'
import source from './App.tsx?raw'

describe('production shell', () => {
  it('does not expose a demo environment banner', () => {
    expect(source).not.toContain('演示环境')
  })
})
