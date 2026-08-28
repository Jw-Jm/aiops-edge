import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import GraphMap from './GraphMap'
import { Graph } from '@antv/g6'

vi.mock('@antv/g6', () => ({
  Graph: vi.fn().mockImplementation(() => ({ render: vi.fn(), destroy: vi.fn() })),
}))

describe('GraphMap', () => {
  it('uses a deterministic bounded map layout and never force layout', () => {
    render(<GraphMap subgraph={{ center_entity_uid: 'a', vertices: [], edges: [], meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: false, stale: false, generated_at: '', warning_codes: [] } }} />)
    const calls = vi.mocked(Graph).mock.calls
    const options = calls[calls.length - 1]?.[0] as any
    expect(options.layout.type).not.toBe('force')
    expect(['dagre', 'grid']).toContain(options.layout.type)
    expect(options.animation).toBe(false)
  })
})
