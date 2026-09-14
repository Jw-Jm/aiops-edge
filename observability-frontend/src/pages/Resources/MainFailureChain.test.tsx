import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import MainFailureChain from './MainFailureChain'

describe('MainFailureChain', () => {
  const resource = (type: string, uid: string, name: string) => ({ clusterId: 'cluster-a', uid, type, domain: 'application' as const, name })
  const center = resource('service', 'checkout', 'checkout')
  it('shows one direct-neighbor chain by default and can expand it', async () => {
    render(<MainFailureChain center={center} upstream={[resource('service', 'web', 'web'), resource('service', 'edge', 'edge')]} downstream={[resource('middleware', 'mysql', 'mysql'), resource('middleware', 'redis', 'redis')]} />)
    expect(screen.getAllByTestId('main-chain-node')).toHaveLength(3)
    const expand = screen.getByRole('button', { name: '展开上下游' })
    await expand.click()
    expect(screen.getAllByTestId('main-chain-node')).toHaveLength(5)
  })
})
