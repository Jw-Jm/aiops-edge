import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import MainFailureChain from './MainFailureChain'

describe('MainFailureChain', () => {
  const center = { type: 'service', id: 'checkout', label: 'checkout' }
  it('shows one direct-neighbor chain by default and can expand it', async () => {
    render(<MainFailureChain center={center} upstream={[{ type: 'service', id: 'web', label: 'web' }, { type: 'service', id: 'edge', label: 'edge' }]} downstream={[{ type: 'db', id: 'mysql', label: 'mysql' }, { type: 'cache', id: 'redis', label: 'redis' }]} />)
    expect(screen.getAllByTestId('main-chain-node')).toHaveLength(3)
    const expand = screen.getByRole('button', { name: '展开上下游' })
    await expand.click()
    expect(screen.getAllByTestId('main-chain-node')).toHaveLength(5)
  })
})
