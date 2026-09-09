import { describe, expect, it } from 'vitest'
import source from './IntelligentInvestigation.tsx?raw'

describe('historical investigation Graph Context', () => {
  it('renders a partial graph context response instead of treating it as a missing request', () => {
    expect(source).toContain('getRunGraphContext(runId)')
    expect(source).toContain('GraphContextPanel context={graphContext}')
  })

  it('keeps the Run view read-only and renders the persisted frozen snapshot', () => {
    expect(source).toContain('ScopeBar snapshot=')
    expect(source).toContain('viewModel.scope.timeRange')
    expect(source).not.toContain('setResource(')
    expect(source).not.toContain('switchCluster(')
    expect(source).not.toContain('setTimeRange(')
  })
})
