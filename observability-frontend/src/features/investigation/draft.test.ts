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

  it('round-trips a typed resource without serializing the object as [object Object]', () => {
    const draft: InvestigationDraft = {
      source: 'resource', clusterId: 'cluster-a', namespace: 'payment',
      resourceId: 'svc/payment-api', targetType: 'service', symptom: '分析错误率突增',
      resource: {
        clusterId: 'cluster-a', uid: 'svc/payment-api', type: 'service', domain: 'application', name: 'payment-api',
      },
    }
    const params = draftToSearchParams(draft)
    expect(params.get('resource')).toBe('svc/payment-api')
    expect(params.get('resourceType')).toBe('service')
    expect(params.get('resourceName')).toBe('payment-api')
    expect(params.toString()).not.toContain('%5Bobject+Object%5D')
    expect(draftFromSearchParams(params)).toEqual(draft)
  })

  it('projects legacy resource parameters to a typed resource only for selectable types', () => {
    const draft = draftFromSearchParams(new URLSearchParams({
      source: 'chat', clusterId: 'cluster-a', resource: 'deployment/payment', resourceType: 'deployment',
      resourceName: 'payment', namespace: 'payment', symptom: 'pods pending',
    }))
    expect(draft.resource).toMatchObject({
      clusterId: 'cluster-a', uid: 'deployment/payment', type: 'deployment', domain: 'kubernetes', name: 'payment', namespace: 'payment',
    })
    expect(draftFromSearchParams(new URLSearchParams({ clusterId: 'cluster-a', resource: 'k8s-cluster-a', resourceType: 'k8s_cluster' })).resource).toBeUndefined()
  })
})
