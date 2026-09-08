import { describe, expect, it } from 'vitest'
import { draftFromSearchParams, draftToSearchParams, type InvestigationDraft } from './draft'

describe('investigation draft', () => {
  it('round-trips only explicit source context', () => {
    const draft: InvestigationDraft = {
      source: 'chat', clusterId: 'cluster-a', namespace: 'payment',
      resourceId: 'payment-api', targetType: 'service', symptom: '分析错误率突增',
    }
    expect(draftFromSearchParams(draftToSearchParams(draft))).toEqual(draft)
  })
})
