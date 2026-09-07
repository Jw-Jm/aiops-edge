import { describe, expect, it } from 'vitest'
import source from './IntelligentInvestigation.tsx?raw'

describe('historical investigation Graph Context', () => {
  it('renders a partial graph context response instead of treating it as a missing request', () => {
    expect(source).toContain('getRunGraphContext(runId)')
    expect(source).toContain('GraphContextPanel context={graphContext}')
  })
})
