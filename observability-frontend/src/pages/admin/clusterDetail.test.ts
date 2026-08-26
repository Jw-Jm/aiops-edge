import { describe, expect, it } from 'vitest'
import { clusterDetailError } from './clusterDetail'

describe('cluster detail response semantics', () => {
  it('preserves a backend unavailable/error reason instead of treating it as an empty list', () => {
    expect(clusterDetailError({ error: 'cluster has no kubeconfig, cannot query nodes' }))
      .toBe('cluster has no kubeconfig, cannot query nodes')
  })

  it('returns an empty error for a real empty successful response', () => {
    expect(clusterDetailError({ nodes: [] })).toBe('')
  })
})
