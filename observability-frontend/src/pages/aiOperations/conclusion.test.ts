import { describe, expect, it } from 'vitest'
import { allowsActionDraft, deriveConclusion, projectActionStage, type ConclusionInput } from './conclusion'

const base: ConclusionInput = {
  rootCause: 'Pod OOMKilled 导致容器反复重启',
  confidence: 0.92,
  evidenceCount: 6,
  hasUnavailableSource: false,
  partial: false,
  stale: false,
  hasUnresolvedContradiction: false,
  hasDirectEvidence: true,
  terminal: true,
}

describe('结论等级推导（不得无依据输出 Confirmed）', () => {
  it('证据充分且终态闭环时才为 Confirmed', () => {
    expect(deriveConclusion(base).level).toBe('Confirmed')
  })

  it('没有候选根因时为 Unknown 并说明缺口', () => {
    const view = deriveConclusion({ ...base, rootCause: null })
    expect(view.level).toBe('Unknown')
    expect(view.missing.length).toBeGreaterThan(0)
  })

  it('没有任何证据时不得确认根因', () => {
    const view = deriveConclusion({ ...base, evidenceCount: 0 })
    expect(view.level).toBe('Unknown')
    expect(view.downgradeReasons.join()).toContain('证据')
  })

  it('存在未解决关键反证时降级为 Candidate', () => {
    const view = deriveConclusion({ ...base, hasUnresolvedContradiction: true })
    expect(view.level).toBe('Candidate')
    expect(view.downgradeReasons.join()).toContain('反证')
  })

  it('来源不可用 / Partial / Stale 时一律不得 Confirmed', () => {
    for (const patch of [{ hasUnavailableSource: true }, { partial: true }, { stale: true }]) {
      const view = deriveConclusion({ ...base, ...patch })
      expect(view.level).not.toBe('Confirmed')
      expect(view.level).toBe('Candidate')
    }
  })

  it('置信度未达门槛时逐级降级', () => {
    expect(deriveConclusion({ ...base, confidence: 0.42 }).level).toBe('Candidate')
    expect(deriveConclusion({ ...base, confidence: 0.7 }).level).toBe('Supported')
  })

  it('高分但缺直接证据时不得 Confirmed', () => {
    const view = deriveConclusion({ ...base, hasDirectEvidence: false })
    expect(view.level).toBe('Supported')
    expect(view.downgradeReasons.join()).toContain('直接支持证据')
  })

  it('Non-terminal 运行不得 Confirmed（未形成恢复闭环）', () => {
    expect(deriveConclusion({ ...base, terminal: false }).level).toBe('Supported')
  })

  it('服务端未给置信度时不得 Confirmed', () => {
    const view = deriveConclusion({ ...base, confidence: null })
    expect(view.level).toBe('Candidate')
    expect(view.missing.join()).toContain('置信度')
  })

  it('Unknown 不允许生成受控动作草稿', () => {
    expect(allowsActionDraft('Unknown')).toBe(false)
    expect(allowsActionDraft('Candidate')).toBe(false)
    expect(allowsActionDraft('Supported')).toBe(true)
    expect(allowsActionDraft('Confirmed')).toBe(true)
  })
})

describe('动作生命周期投影（不产生伪状态）', () => {
  it('未审批动作只显示草稿，不显示执行/验证未来态', () => {
    expect(projectActionStage({ status: 'proposed', preflight_status: 'pending' })).toBe('Draft')
  })

  it('预检通过但未审批显示待确认', () => {
    expect(projectActionStage({ status: 'proposed', preflight_status: 'passed' })).toBe('AwaitingConfirmation')
  })

  it('已审批进入执行前阶段', () => {
    expect(projectActionStage({ status: 'approved', preflight_status: 'passed' })).toBe('Preflight')
  })

  it('执行与验证状态按真实结果映射', () => {
    expect(projectActionStage({ status: 'approved', execution_status: 'running' })).toBe('Running')
    expect(projectActionStage({ status: 'approved', execution_status: 'succeeded' })).toBe('Succeeded')
    expect(projectActionStage({ status: 'approved', execution_status: 'failed' })).toBe('Failed')
    expect(projectActionStage({ status: 'approved', execution_status: 'succeeded', verification_status: 'verified' })).toBe('Verified')
    expect(projectActionStage({ status: 'approved', execution_status: 'rolled_back' })).toBe('RolledBack')
  })

  it('被拒绝的动作回到草稿态而不是错误状态', () => {
    expect(projectActionStage({ status: 'rejected' })).toBe('Draft')
  })
})
