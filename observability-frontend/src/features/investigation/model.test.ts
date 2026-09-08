import { describe, expect, it } from 'vitest'
import { toInvestigationViewModel } from './model'

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
        { evidence_id: 'e2', observed_at: '2026-09-08T20:31:00Z', type: 'metrics', source: 'prometheus', fact: 'newer', source_reliability: 0.96 },
      ], hypotheses: [], created_at: '2026-09-08T20:00:00Z',
    })
    expect(vm.evidence.map((item) => item.id)).toEqual(['e2', 'e1'])
    expect(vm.evidence[0]).toMatchObject({ source: 'prometheus', reliability: 0.96 })
  })
})
