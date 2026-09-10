import { describe, expect, it } from 'vitest'
import source from './ResourceRelationships.tsx?raw'

describe('resource relationship explorer', () => {
  it('searches typed resources in the active cluster instead of a service-only graph', () => {
    expect(source).toContain('isSelectableResourceType')
    expect(source).toContain('resourceDomainOf')
    expect(source).toContain('item.cluster_id === activeClusterId')
    expect(source).toContain('max_vertices: 80')
    expect(source).toContain('max_edges: 200')
    expect(source).not.toContain("entity_type: 'service'")
  })
})
