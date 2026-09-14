import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { RawDataPanel } from './RawDataPanel'

describe('RawDataPanel', () => {
  it('is collapsed by default, formats JSON and supports copy', async () => {
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
    render(<RawDataPanel data={{ cluster_id: 'cluster-a', status: 'healthy' }} />)
    expect(screen.queryByRole('button', { name: '复制' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '原始数据' }))
    expect(screen.getByText(/"cluster_id": "cluster-a"/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '复制' }))
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('cluster-a'))
  })

  it('truncates payloads above the 200KB safety limit', () => {
    render(<RawDataPanel data={{ payload: 'x'.repeat(210 * 1024) }} />)
    fireEvent.click(screen.getByRole('button', { name: '原始数据' }))
    expect(screen.getByText(/超过 200KB/)).toBeInTheDocument()
    expect(screen.getByText('原始数据超过 200KB，已截断显示')).toBeInTheDocument()
  })
})
