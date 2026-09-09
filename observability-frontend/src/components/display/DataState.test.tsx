import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { DataState } from './DataState'

describe('DataState', () => {
  it('renders all six states with a stable accessible status', () => {
    for (const kind of ['loading', 'empty', 'error', 'partial', 'stale', 'forbidden'] as const) {
      const { unmount } = render(<DataState kind={kind} />)
      expect(screen.getByRole(kind === 'error' || kind === 'forbidden' ? 'alert' : 'status')).toBeInTheDocument()
      unmount()
    }
  })

  it('allows an error state to be retried', () => {
    const onRetry = vi.fn()
    render(<DataState kind="error" title="读取失败" onRetry={onRetry} />)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })
})
