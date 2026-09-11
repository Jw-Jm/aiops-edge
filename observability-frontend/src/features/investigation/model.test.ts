import { describe, expect, it } from 'vitest'
import { toInvestigationViewModel } from './model'
import { terminationReasonLabel } from '../workflow/statusPresentation'

describe('investigation view model', () => {
  it('does not promote an unconfirmed root cause when evidence is insufficient', () => {
    const vm = toInvestigationViewModel({
      run_id: 'run-1', tenant_id: 'tenant-a', primary_cluster_id: 'cluster-a',
      target_resource_id: 'payment-api', intent: '分析错误率', status: 'investigating',
      root_cause: '', confidence: 0.3, evidence: [], hypotheses: [],
      created_at: '2026-09-08T20:00:00Z',
    })
    expect(vm.conclusion.state).toBe('insufficient_evidence')
    expect(vm.conclusion.title).toBe('证据不足，尚不能确认根因')
  })

  it('sorts evidence newest first and preserves provenance', () => {
    const vm = toInvestigationViewModel({
      run_id: 'run-1', tenant_id: 'tenant-a', primary_cluster_id: 'cluster-a',
      target_resource_id: 'payment-api', intent: '分析错误率', status: 'success',
      root_cause: '配置变更', confidence: 0.9,
      evidence: [
        { evidence_id: 'e1', observed_at: '2026-09-08T20:29:00Z', type: 'logs', source: 'loki', fact: 'older', source_reliability: 0.8 },
        { evidence_id: 'e2', observed_at: '2026-09-08T20:31:00Z', type: 'metrics', source: 'prometheus', fact: 'newer', source_reliability: 0.96, quality: 'complete', supports: ['h1'] },
      ], hypotheses: [], created_at: '2026-09-08T20:00:00Z',
    })
    expect(vm.evidence.map((item) => item.id)).toEqual(['e2', 'e1'])
    expect(vm.evidence[0]).toMatchObject({ source: 'prometheus', reliability: 0.96, quality: 'complete', supports: ['h1'] })
  })

  it('projects a selectable typed resource and freezes the absolute window', () => {
    const model = toInvestigationViewModel({
      run_id: 'run-2', tenant_id: 'tenant-a', primary_cluster_id: 'cluster-a',
      target_resource_id: 'deployment/payment', target_resource_type: 'deployment', namespace: 'payment',
      query_window_start: '2026-09-09T00:00:00.000Z', query_window_end: '2026-09-09T01:00:00.000Z',
      confidence: 0.92, root_cause: '数据库连接池耗尽', evidence: [{ evidence_id: 'e-1', fact: 'pool saturated' }],
    })
    expect(model.scope.resource).toMatchObject({
      clusterId: 'cluster-a', uid: 'deployment/payment', type: 'deployment', domain: 'kubernetes', namespace: 'payment',
    })
    expect(model.scope.timeRange).toEqual({ mode: 'absolute', start: '2026-09-09T00:00:00.000Z', end: '2026-09-09T01:00:00.000Z' })
    expect(model.conclusion).toMatchObject({ state: 'confirmed', title: '数据库连接池耗尽' })
  })

  it('does not fabricate k8s_cluster as a platform resource and never states a root cause with insufficient evidence', () => {
    const model = toInvestigationViewModel({
      run_id: 'run-3', tenant_id: 'tenant-a', primary_cluster_id: 'cluster-a',
      target_resource_id: 'cluster-a', target_resource_type: 'k8s_cluster',
      confidence: 0.98, root_cause: '可能是节点抖动', evidence: [],
    })
    expect(model.scope.resource).toBeUndefined()
    expect(model.conclusion.state).toBe('insufficient_evidence')
    expect(model.conclusion.title).not.toContain('可能是节点抖动')
    expect(model.conclusion.rootCause).toBe('')
  })
})

describe('investigation summary binding (Task 11)', () => {
  it('prefers the server persisted summary over local guesses', () => {
    const vm = toInvestigationViewModel({
      run_id: 'run-9', tenant_id: 'tenant-a', primary_cluster_id: 'cluster-a',
      target_resource_id: 'deployment/payment', target_resource_type: 'deployment',
      status: 'partial', root_cause: '', confidence: 0.4,
      evidence: [{ evidence_id: 'ev-1', fact: 'pool saturated' }],
      investigation_summary: {
        schema_version: 1,
        termination_reason: 'budget_exhausted',
        budget_summary: { max_steps: 10, max_tools: 16, consumed_steps: 10, consumed_tools: 16 },
        causal_chain: [{ source_uid: 'u1', source_name: '配置变更', relation_label: '触发',
          target_uid: 'u2', target_name: '连接池耗尽', fact_status: 'fact' }],
        next_verification: ['复查连接池上限'],
      },
    })
    expect(vm.summary.terminationReason).toBe('budget_exhausted')
    expect(vm.summary.budget.consumedTools).toBe(16)
    expect(vm.summary.causalChain).toHaveLength(1)
    expect(vm.summary.causalChain[0]).toMatchObject({ source: '配置变更', relation: '触发', target: '连接池耗尽', factStatus: 'fact' })
    expect(vm.summary.nextVerification).toEqual(['复查连接池上限'])
  })

  it('renders each termination reason with the contractual copy', () => {
    for (const [reason, fragment] of [
      ['root_confirmed', '确认根因'],
      ['evidence_exhausted', '仍不足以确认'],
      ['budget_exhausted', '预算'],
      ['source_unavailable', '不可用'],
      ['cancelled', '取消'],
      ['runtime_failed', '运行失败'],
    ] as const) {
      expect(terminationReasonLabel(reason)).toContain(fragment)
    }
    // partial 不是终止原因。
    expect(terminationReasonLabel('partial')).toBe('终止原因未提供')
  })
})
