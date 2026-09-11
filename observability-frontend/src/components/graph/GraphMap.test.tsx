import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import GraphMap from './GraphMap'
import { Graph } from '@antv/g6'

vi.mock('@antv/g6', () => ({
  Graph: vi.fn().mockImplementation(() => ({ render: vi.fn(), destroy: vi.fn(), on: vi.fn(), fitView: vi.fn() })),
}))

describe('GraphMap', () => {
  function lastOptions() {
    const calls = vi.mocked(Graph).mock.calls
    return calls[calls.length - 1]?.[0] as any
  }

  it('uses a deterministic bounded map layout and never force layout', () => {
    render(<GraphMap subgraph={{ center_entity_uid: 'a', vertices: [], edges: [], meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: false, stale: false, generated_at: '', warning_codes: [] } }} />)
    const options = lastOptions()
    expect(options.layout.type).not.toBe('force')
    expect(['dagre', 'grid']).toContain(options.layout.type)
    expect(options.animation).toBe(false)
  })

  it('renders readable node/edge geometry with explicit relation labels', () => {
    render(<GraphMap subgraph={{ center_entity_uid: 'a', vertices: [], edges: [], meta: { contract_version: 'graph-dto-v1', schema_version: 2, partial: false, stale: false, generated_at: '', warning_codes: [] } }} />)
    const options = lastOptions()
    // 节点最小尺寸与正文最小字号：保证节点名与关系名无需猜色即可读取。
    expect(options.node.style.size).toEqual([196, 64])
    expect(options.node.style.labelFontSize).toBeGreaterThanOrEqual(12)
    expect(options.node.style.labelMaxWidth).toBeGreaterThanOrEqual(150)
    // 箭头不小于 10px；关系标签 12px 且使用不透明底色。
    expect(options.edge.style.labelFontSize).toBeGreaterThanOrEqual(12)
    expect(options.edge.style.endArrowSize).toBeGreaterThanOrEqual(10)
    expect(options.edge.style.labelBackgroundOpacity).toBe(1)
    // 层间距保证关系标签不重叠。
    expect(options.layout.ranksep).toBeGreaterThanOrEqual(120)
    expect(options.behaviors).toEqual(expect.arrayContaining(['drag-canvas', 'zoom-canvas']))
  })
})
