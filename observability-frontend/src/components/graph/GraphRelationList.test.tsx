import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import GraphRelationList from './GraphRelationList'

describe('GraphRelationList', () => {
  it('supports keyboard focus and stable relation ordering', () => {
    const onLocate = vi.fn()
    render(<GraphRelationList rows={[{ id: 'e-1', source: 'a', target: 'b', sourceUid: 'a', targetUid: 'b', relationType: 'DEPENDS_ON', sourceName: '服务 A', targetName: '节点 B', label: '依赖', style: 'structural', propagatesFailure: false, factStatus: 'fact', sourceRef: 'spec.dependsOn', syncedAt: '' }]} onLocate={onLocate} />)
    const row = screen.getByRole('button', { name: /服务 A.*依赖.*节点 B/ })
    row.focus()
    fireEvent.keyDown(row, { key: 'Enter' })
    expect(onLocate).toHaveBeenCalledWith('e-1')
    expect(screen.getByRole('button', { name: '等价关系列表' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '等价关系列表' }))
    expect(screen.getByRole('region', { name: '等价关系列表' })).toBeInTheDocument()
  })
})
