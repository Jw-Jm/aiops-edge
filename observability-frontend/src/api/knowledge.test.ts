import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { createKnowledge, listKnowledge, searchKnowledge } from './knowledge'

afterEach(() => vi.restoreAllMocks())

describe('knowledge API client', () => {
  it('keeps list and search requests under the selected cluster scope', async () => {
    vi.spyOn(api, 'get').mockResolvedValueOnce({ data: { items: [], meta: { index_available: true } } } as never)
    vi.spyOn(api, 'post').mockResolvedValueOnce({ data: { items: [] } } as never)

    await listKnowledge('cluster-a', { knowledgeType: 'incident', status: 'published' })
    await searchKnowledge('cluster-a', { query: 'pod crashloop', topK: 8 })

    expect(api.get).toHaveBeenCalledWith('/clusters/cluster-a/knowledge', {
      params: { knowledge_type: 'incident', status: 'published' },
    })
    expect(api.post).toHaveBeenCalledWith('/clusters/cluster-a/knowledge/search', { query: 'pod crashloop', top_k: 8 })
  })

  it('sends a draft without accepting a client tenant identifier', async () => {
    vi.spyOn(api, 'post').mockResolvedValueOnce({ data: { data: { knowledge_id: 'k-1' } } } as never)

    await createKnowledge('cluster-a', {
      scope_type: 'cluster',
      cluster_id: 'cluster-a',
      knowledge_type: 'incident',
      title: 'Pod 未就绪',
      summary: '处置路径',
      content: '检查节点和容器运行态',
      source_kind: 'manual',
      status: 'draft',
    })

    expect(api.post).toHaveBeenCalledWith('/clusters/cluster-a/knowledge', expect.objectContaining({
      cluster_id: 'cluster-a',
      status: 'draft',
    }))
    expect(api.post).not.toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ tenant_id: expect.anything() }))
  })
})
