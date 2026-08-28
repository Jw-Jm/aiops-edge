import { describe, expect, it, vi } from 'vitest'
import { getGraphCandidate, getGraphNeighbors, getGraphPath } from './knowledgeGraph'
import { api } from './client'

describe('knowledge graph API', () => {
  it('uses query-api typed graph routes', async () => {
    const spy = vi.spyOn(api, 'get').mockResolvedValue({ data: {} } as any)
    await getGraphNeighbors('service:v1:tenant:one', { depth: 2 })
    expect(spy).toHaveBeenCalledWith('/ai/kg/entities/service%3Av1%3Atenant%3Aone/neighbors', { params: { depth: 2 } })
    spy.mockRestore()
  })
  it('posts a typed path request without query language', async () => {
    const spy = vi.spyOn(api, 'post').mockResolvedValue({ data: {} } as any)
    await getGraphPath('a', 'b')
    expect(spy).toHaveBeenCalledWith('/ai/kg/path', { source_entity_uid: 'a', target_entity_uid: 'b', max_depth: 6 })
    spy.mockRestore()
  })
  it('exposes the bounded RCA candidate route as a typed graph call', async () => {
    const spy = vi.spyOn(api, 'get').mockResolvedValue({ data: {} } as any)
    await getGraphCandidate('service:v1', { depth: 2, max_vertices: 300, max_edges: 1000 })
    expect(spy).toHaveBeenCalledWith('/ai/kg/entities/service%3Av1/candidate', { params: { depth: 2, max_vertices: 300, max_edges: 1000 } })
    spy.mockRestore()
  })
})
