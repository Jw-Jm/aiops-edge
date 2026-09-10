import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { BoundedDataRegion } from './BoundedDataRegion'

describe('BoundedDataRegion', () => {
  it('shows the explicit seven-state contract and retries only errors', () => {
    const onRetry = vi.fn()
    const { rerender } = render(<BoundedDataRegion state="error" onRetry={onRetry}>facts</BoundedDataRegion>)
    expect(screen.getByTestId('bounded-data-region')).toHaveAttribute('data-state', 'error')
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(onRetry).toHaveBeenCalledOnce()
    rerender(<BoundedDataRegion state="forbidden" onRetry={onRetry}>facts</BoundedDataRegion>)
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument()
  })
})
