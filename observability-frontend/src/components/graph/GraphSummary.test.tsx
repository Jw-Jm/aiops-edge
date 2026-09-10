import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import GraphSummary from './GraphSummary'

describe('GraphSummary', () => {
  it('shows partial and stale as distinct states', () => {
    render(<GraphSummary health={{ ready: true, backend: 'hugegraph', schema_version: 2 }} subgraph={{ center_entity_uid: 'a', vertices: [], edges: [], meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: true, stale: true, generated_at: '', warning_codes: ['LAG'] } }} />)
    expect(screen.getByText('部分结果')).toBeInTheDocument()
    expect(screen.getByText('数据陈旧')).toBeInTheDocument()
  })

  it('shows truthful aggregate totals when the visual budget is exceeded', () => {
    render(<GraphSummary subgraph={{ center_entity_uid: 'a', vertices: [], edges: [], total_nodes: 2418, total_edges: 4806, aggregated: true, meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: true, stale: false, generated_at: '', warning_codes: [] } }} />)
    expect(screen.getByText(/匹配总数 2,418 节点 \/ 4,806 关系/)).toBeInTheDocument()
  })
})
