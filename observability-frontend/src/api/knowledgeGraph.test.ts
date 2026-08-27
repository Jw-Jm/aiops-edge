import { describe, expect, it, vi } from 'vitest'
import { getGraphNeighbors, getGraphPath } from './knowledgeGraph'
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
})
