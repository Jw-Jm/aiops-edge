import { describe, expect, it } from 'vitest'
import source from './ResourceRelationships.tsx?raw'

describe('resource relationship explorer', () => {
  it('searches typed resources in the active cluster instead of a service-only graph', () => {
    expect(source).toContain('isSelectableResourceType')
    expect(source).toContain('resourceDomainOf')
    expect(source).toContain('item.cluster_id === activeClusterId')
    expect(source).toContain('max_vertices: 300')
    expect(source).toContain('max_edges: 1000')
    expect(source).not.toContain("entity_type: 'service'")
  })

  it('renders structured API errors as text instead of crashing React', () => {
    expect(source).toContain('function graphRequestErrorMessage')
    expect(source).toContain('typeof error.message === \'string\'')
    expect(source).toContain('setError(graphRequestErrorMessage(requestError))')
  })
})
