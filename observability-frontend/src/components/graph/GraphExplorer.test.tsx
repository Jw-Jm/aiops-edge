import { describe, expect, it } from 'vitest'
import source from './GraphExplorer.tsx?raw'

describe('graph explorer relationship inspection', () => {
  it('provides a readable selected-edge inspector from the equivalent relation list', () => {
    expect(source).toContain('selectedEdge')
    expect(source).toContain('关系检查器')
    expect(source).toContain('onLocate={(edgeId)')
    // 检查器必须给出权威字段与同步时间，让验收人不靠猜色即可判断来源。
    expect(source).toContain('权威字段')
    expect(source).toContain('同步时间')
    expect(source).toContain('箭头表示源资源指向目标资源')
  })

  it('exposes the seven relation semantics as filters without rewriting facts', () => {
    expect(source).toContain('RELATION_SEMANTICS.map')
    expect(source).toContain('RELATION_SEMANTIC_LABELS')
    expect(source).toContain('enabledSemantics')
    expect(source).toContain('relationSemantic')
    expect(source).toContain('filterBySemantics')
  })

  it('keeps the canvas wide at 1024px and moves the inspector into a drawer', () => {
    expect(source).toContain('graph-inline-inspector')
    expect(source).toContain('graph-inspector-trigger')
    expect(source).toContain('查看关系')
    expect(source).toContain('Drawer')
  })

  it('supports node focus, context node expansion and clearing the focus', () => {
    expect(source).toContain('focusGraph')
    expect(source).toContain('onNodeSelect')
    expect(source).toContain('onCanvasSelect')
    expect(source).toContain('showContextNodes')
    expect(source).toContain('清除聚焦')
  })
})
