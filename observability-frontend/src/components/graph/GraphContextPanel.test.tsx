import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import GraphContextPanel from './GraphContextPanel'

describe('GraphContextPanel', () => {
  it('distinguishes persisted partial and stale context', () => {
    render(<GraphContextPanel context={{ context_version: 3, graph_schema_version: 2, partial: true, stale: true, warning_codes: ['LAG'], vertices: [{}], edges: [], propagation_paths: [] }} />)
    expect(screen.getByText('部分结果')).toBeInTheDocument()
    expect(screen.getByText('数据陈旧')).toBeInTheDocument()
    expect(screen.getByText(/Graph Context 警告：LAG/)).toBeInTheDocument()
  })
})
