import { describe, expect, it } from 'vitest'
import { canControlResource, canDecideAction, toActionViewModel } from './actionModel'

const action = (overrides: Record<string, unknown> = {}) => ({
  action_id: 'action-1', run_id: 'run-1', action_type: 'rollout_restart', action_hash: 'hash',
  hash_schema_version: 2, action_version: 1, preflight_status: 'passed',
  target_resource_type: 'deployment', status: 'proposed', dry_run: false,
  target_name: 'payment-api', target_uid: 'uid-1', resource_version: '7', namespace: 'payment',
  operation: 'rollout_restart', execution_status: 'not_started', created_at: '2026-09-08T20:00:00Z',
  ...overrides,
})

describe('action projection', () => {
  it('keeps missing risk explicit and links the source run', () => {
    expect(toActionViewModel(action()).risk).toEqual({ level: 'unknown', label: '未评估' })
    expect(toActionViewModel(action()).runHref).toBe('/investigation/run-1')
  })

  it('allows decisions only for approvers and administrators', () => {
    expect(canDecideAction('operator')).toBe(false)
    expect(canDecideAction('approver')).toBe(true)
    expect(canDecideAction('admin')).toBe(true)
  })

  it('requires a returned control capability for mutable resource types', () => {
    expect(canControlResource('physical_server', [])).toBe(false)
    expect(canControlResource('physical_server', ['hardware.action.execute'])).toBe(true)
    expect(canControlResource('k8s_node', ['kubernetes.node.write'])).toBe(true)
    expect(canControlResource('deployment', ['kubernetes.workload.write'])).toBe(true)
    expect(canControlResource('vm', [])).toBe(false)
    expect(canControlResource('pvc', ['storage.read'])).toBe(false)
  })
})
