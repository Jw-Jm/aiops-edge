import { describe, expect, it } from 'vitest'
import { formatStructuredValue } from './structuredDisplay'

describe('formatStructuredValue', () => {
  it('renders nested values as readable key/value lines instead of raw JSON', () => {
    expect(formatStructuredValue({ replicas: 2, labels: { tier: 'api' } })).toContain('replicas: 2')
    expect(formatStructuredValue({ replicas: 2, labels: { tier: 'api' } })).toContain('tier: api')
    expect(formatStructuredValue({ replicas: 2 })).not.toContain('"replicas"')
  })
})

