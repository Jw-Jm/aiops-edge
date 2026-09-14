import { describe, expect, it } from 'vitest'
import {
  actionStatusLabel,
  executionStatusLabel,
  investigationSourceLabel,
  investigationStatusLabel,
  terminationReasonLabel,
  verificationStatusLabel,
} from './statusPresentation'

describe('workflow status presentation', () => {
  it.each([
    ['awaiting_approval', '待审批'],
    ['investigating', '调查中'],
    ['failed', '失败'],
    ['cancelled', '已取消'],
    ['success', '已完成'],
    ['partial', '部分完成'],
    ['regressed', '发生回归'],
  ])('maps investigation status %s', (input, expected) => {
    expect(investigationStatusLabel(input)).toBe(expected)
  })

  it.each([
    ['proposed', '待审批'],
    ['approved', '已批准'],
    ['running', '执行中'],
    ['succeeded', '已成功'],
    ['failed', '失败'],
    ['rejected', '已拒绝'],
  ])('maps action status %s', (input, expected) => {
    expect(actionStatusLabel(input)).toBe(expected)
  })

  it.each([
    ['not_started', '未执行'],
    ['running', '执行中'],
    ['succeeded', '执行成功'],
    ['failed', '执行失败'],
    ['regressed', '发生回归'],
  ])('maps execution status %s', (input, expected) => {
    expect(executionStatusLabel(input)).toBe(expected)
  })

  it.each([
    ['pending', '待验证'],
    ['verifying', '验证中'],
    ['passed', '验证通过'],
    ['failed', '验证失败'],
  ])('maps verification status %s', (input, expected) => {
    expect(verificationStatusLabel(input)).toBe(expected)
  })

  it('never leaks a raw internal status code to the user', () => {
    expect(investigationStatusLabel('some_new_status')).toBe('未知')
    expect(actionStatusLabel('some_new_status')).toBe('未知')
    expect(executionStatusLabel('some_new_status')).toBe('未知')
    expect(investigationStatusLabel('awaiting_approval')).not.toContain('_')
  })

  it('renders the six stable termination reasons and never uses partial', () => {
    expect(terminationReasonLabel('root_confirmed')).toBe('已取得足够证据并确认根因')
    expect(terminationReasonLabel('evidence_exhausted')).toBe('可用证据已查询完，仍不足以确认')
    expect(terminationReasonLabel('budget_exhausted')).toContain('预算')
    expect(terminationReasonLabel('source_unavailable')).toBe('必要数据源不可用')
    expect(terminationReasonLabel('cancelled')).toBe('调查已取消')
    expect(terminationReasonLabel('runtime_failed')).toBe('调查运行失败')
    expect(terminationReasonLabel('partial')).not.toContain('partial')
  })

  it('labels investigation sources without a synthetic system identity', () => {
    expect(investigationSourceLabel('human')).toBe('人工发起')
    expect(investigationSourceLabel('system_suggested')).toBe('系统建议')
    expect(investigationSourceLabel('system_auto')).toBe('系统自动')
    expect(investigationSourceLabel('unknown')).toBe('来源未知')
  })
})
