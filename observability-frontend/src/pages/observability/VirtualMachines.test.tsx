import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import VirtualMachines from './VirtualMachines'
import { listVms } from '../../api/client'

vi.mock('../../api/client', () => ({ listVms: vi.fn(), getVm: vi.fn() }))
vi.mock('../../store/uiStore', () => ({
  useUIStore: (selector: (state: { currentClusterId: string }) => unknown) => selector({ currentClusterId: 'all' }),
}))

describe('VirtualMachines authentic failure states', () => {
  beforeEach(() => {
    const failure = Promise.reject(new Error('KubeVirt backend unavailable'))
    // Keep the fixture rejection handled at the test boundary as well as by
    // the component, otherwise Vitest reports the intentionally failed API
    // call as an unhandled rejection before React flushes the error state.
    failure.catch(() => undefined)
    vi.mocked(listVms).mockReturnValue(failure as never)
  })

  it('shows an error state instead of a misleading empty VM list', async () => {
    render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}><VirtualMachines /></MemoryRouter>)
    expect(await screen.findByText('KubeVirt backend unavailable')).toBeInTheDocument()
  })
})
