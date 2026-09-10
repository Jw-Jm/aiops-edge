import { describe, expect, it } from 'vitest'
import source from './GraphExplorer.tsx?raw'

describe('graph explorer relationship inspection', () => {
  it('provides a readable selected-edge inspector from the equivalent relation list', () => {
    expect(source).toContain('selectedEdge')
    expect(source).toContain('关系检查器')
    expect(source).toContain('onLocate={(edgeId)')
    expect(source).toContain('事实来源')
  })
})
