import { describe, expect, it } from 'vitest'
import { actionPhase, dataStatusLabel, healthLabel } from './readModels'

describe('read model state rules', () => {
  it('derives ActionPhase from authoritative lifecycle fields', () => {
    expect(actionPhase({ approval_status: 'pending' })).toBe('awaiting_approval')
    expect(actionPhase({ approval_status: 'approved', execution_status: 'not_started' })).toBe('ready_to_execute')
    expect(actionPhase({ execution_status: 'running' })).toBe('executing')
    expect(actionPhase({ execution_status: 'success', verification_status: 'pending' })).toBe('awaiting_verification')
    expect(actionPhase({ verification_status: 'success' })).toBe('completed')
    expect(actionPhase({ execution_status: 'failed' })).toBe('failed')
    expect(actionPhase({ verification_status: 'regressed' })).toBe('regressed')
  })

  it('keeps health and data availability as separate concepts', () => {
    expect(healthLabel('unknown')).toBe('未知')
    expect(dataStatusLabel('unavailable')).toBe('数据不可用')
    expect(dataStatusLabel('stale')).toBe('数据陈旧')
  })
})
