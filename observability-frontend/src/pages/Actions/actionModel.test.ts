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

  it('localizes every workflow status consumed by the action views', () => {
    expect(toActionViewModel(action()).approvalStatus).toBe('待审批')
    expect(toActionViewModel(action()).executionStatus).toBe('未执行')
    expect(toActionViewModel(action()).verificationStatus).toBe('待验证')
    expect(toActionViewModel(action()).preflightStatus).toBe('预检通过')

    const approved = toActionViewModel(action({ status: 'approved', execution_status: 'running', verification_status: 'passed' }))
    expect(approved.approvalStatus).toBe('已批准')
    expect(approved.executionStatus).toBe('执行中')
    expect(approved.verificationStatus).toBe('验证通过')

    const failed = toActionViewModel(action({ status: 'failed', execution_status: 'failed', verification_status: 'failed' }))
    expect(failed.approvalStatus).toBe('失败')
    expect(failed.executionStatus).toBe('执行失败')
    expect(failed.verificationStatus).toBe('验证失败')
    // 不允许裸英文状态泄漏到界面。
    expect(approved.approvalStatus).not.toMatch(/[a-z_]/)
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
