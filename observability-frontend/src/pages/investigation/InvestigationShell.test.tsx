import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import InvestigationShell from './InvestigationShell'
import { toInvestigationViewModel, type InvestigationSnapshotInput } from '../../features/investigation/model'

function snapshot(overrides: Partial<InvestigationSnapshotInput> = {}): InvestigationSnapshotInput {
  return {
    run_id: 'run-1', tenant_id: 'tenant-a', primary_cluster_id: 'cluster-a',
    target_resource_id: 'deployment/payment', target_resource_type: 'deployment', namespace: 'payment',
    query_window_start: '2026-09-09T00:00:00.000Z', query_window_end: '2026-09-09T01:00:00.000Z',
    intent: '分析错误率', status: 'success', root_cause: '数据库连接池耗尽', confidence: 0.92,
    evidence: [
      { evidence_id: 'e-1', observed_at: '2026-09-09T00:10:00Z', fact: '连接池打满', source: 'victoria-metrics', type: 'metric_anomaly', source_reliability: 0.95, quality: 'complete' },
      { evidence_id: 'e-2', observed_at: '2026-09-09T00:20:00Z', fact: '配置变更', source: 'changes', type: 'change', source_reliability: 0.9, quality: 'complete', contradicts: ['h-1'] },
    ],
    hypotheses: [{ hypothesis_id: 'h-1', content: '连接池耗尽', confidence: 0.9, missing_evidence: ['m-1'] }],
    summary_input: {
      termination_reason: 'root_confirmed',
      budget_summary: { max_steps: 10, max_tools: 16, consumed_steps: 6, consumed_tools: 9 },
      next_verification: ['复查连接池上限', '确认变更回滚'],
      causal_chain: [
        { source_uid: 'u1', source_name: '配置变更', relation_label: '触发', target_uid: 'u2', target_name: '连接池耗尽', fact_status: 'fact' },
        { source_uid: 'u2', source_name: '连接池耗尽', relation_label: '疑似导致', target_uid: 'u3', target_name: '请求失败率升高', fact_status: 'inferred' },
      ],
    },
    ...overrides,
  }
}

const renderShell = (input: InvestigationSnapshotInput) => render(
  <InvestigationShell model={toInvestigationViewModel(input)} graphContext={null} tools={[]} />,
)

describe('InvestigationShell summary-first report', () => {
  it('shows conclusion, causal chain and key evidence before technical details', async () => {
    renderShell(snapshot())
    expect(await screen.findByText('根因已确认')).toBeVisible()
    expect(screen.getByText('数据库连接池耗尽')).toBeVisible()
    // 因果链每一跳都有源、中文关系和目标，推断边明确标注。
    expect(screen.getAllByText('配置变更').length).toBeGreaterThan(0)
    expect(screen.getByText('触发')).toBeInTheDocument()
    expect(screen.getAllByText('连接池耗尽').length).toBeGreaterThan(0)
    expect(screen.getByText('推断')).toBeInTheDocument()
    // 右侧可判定信息。
    expect(screen.getByText('证据完整性')).toBeVisible()
    expect(screen.getByText('终止原因与预算')).toBeVisible()
    expect(screen.getByText('已取得足够证据并确认根因')).toBeVisible()
    expect(screen.getByText('6/10')).toBeVisible()
    expect(screen.getByText('9/16')).toBeVisible()
  })

  it('collapses technical details behind a keyboard-expandable section', async () => {
    const user = userEvent.setup()
    renderShell(snapshot())
    await screen.findByText('根因已确认')
    const toggle = screen.getByText(/技术详情/)
    expect(toggle).toBeVisible()
    // 默认收起：完整证据时间线不可见。
    expect(screen.queryByText('证据时间线')).not.toBeInTheDocument()
    await user.click(toggle)
    expect(await screen.findByText('证据时间线')).toBeVisible()
  })

  it('never confirms a root cause for partial or stale evidence', () => {
    const partial = renderShell(snapshot({ partial: true }))
    expect(screen.getByText('根因尚未确认')).toBeVisible()
    expect(screen.queryByText('根因已确认')).not.toBeInTheDocument()
    partial.unmount()

    renderShell(snapshot({ stale: true }))
    expect(screen.getByText('根因尚未确认')).toBeVisible()
    expect(screen.queryByText('根因已确认')).not.toBeInTheDocument()
    // stale 数据必须显式告知，不得伪装为完整数据。
    expect(screen.getAllByText('数据已陈旧').length).toBeGreaterThan(0)
  })
})
