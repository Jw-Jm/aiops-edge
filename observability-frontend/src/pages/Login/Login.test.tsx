import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import Login from './index'

describe('production login page', () => {
  it('does not expose demo environment or demo credentials', () => {
    render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <Login />
      </MemoryRouter>,
    )

    expect(screen.queryByText(/演示环境/)).not.toBeInTheDocument()
    expect(screen.queryByText(/演示账号/)).not.toBeInTheDocument()
    expect(screen.getByText('AIOps Observability')).toBeInTheDocument()
  })
})
