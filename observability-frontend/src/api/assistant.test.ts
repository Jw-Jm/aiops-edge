import { afterEach, describe, expect, it, vi } from 'vitest'
import { createInvestigationDraft, type AssistantConversationScope } from './assistant'

afterEach(() => vi.restoreAllMocks())

describe('assistant API client', () => {
  it('creates a read-only investigation draft with the frozen assistant scope', async () => {
    const scope: AssistantConversationScope = {
      clusterId: 'cluster-a',
      resourceUid: 'uid-order-api',
      from: '2026-09-10T10:00:00Z',
      to: '2026-09-10T11:00:00Z',
      knowledgeScope: 'platform_common_and_current_cluster',
    }

    const result = await createInvestigationDraft({ clusterId: 'cluster-a', resourceUid: 'uid-order-api', scope, status: 'draft' })
    expect(result).toEqual(expect.objectContaining({ status: 'draft', data: expect.objectContaining({
      clusterId: 'cluster-a', resourceUid: 'uid-order-api', scope: expect.objectContaining({ from: scope.from, to: scope.to }),
    }) }))
  })
})
