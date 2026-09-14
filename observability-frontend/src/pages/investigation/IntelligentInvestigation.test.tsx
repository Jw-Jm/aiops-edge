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

  it('passes the server summary and truth metadata into the investigation view model', () => {
    expect(source).toContain('investigation_summary: r.investigation_summary')
    expect(source).toContain('query_window_start: r.time_range_start')
    expect(source).toContain('query_window_end: r.time_range_end')
    expect(source).toContain('partial: r.partial')
    expect(source).toContain('stale: r.stale')
  })
})
