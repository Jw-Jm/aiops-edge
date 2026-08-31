import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import RequireAuth from './RequireAuth'
import { useAuthStore } from '../store/authStore'

describe('RequireAuth', () => {
  beforeEach(() => {
    localStorage.clear()
    useAuthStore.setState({ token: '', username: '', role: '', displayName: '', mustChangePassword: false })
  })

  it('redirects a first-login account to the password change page', () => {
    useAuthStore.setState({ token: 'session-token', mustChangePassword: true })

    render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }} initialEntries={['/overview']}>
        <Routes>
          <Route path="/change-password" element={<div>change-password-page</div>} />
          <Route path="/overview" element={<RequireAuth><div>overview-page</div></RequireAuth>} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByText('change-password-page')).toBeInTheDocument()
    expect(screen.queryByText('overview-page')).not.toBeInTheDocument()
  })

  it('allows the forced-change route itself', () => {
    useAuthStore.setState({ token: 'session-token', mustChangePassword: true })

    render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }} initialEntries={['/change-password']}>
        <Routes>
          <Route path="/change-password" element={<RequireAuth><div>change-password-page</div></RequireAuth>} />
          <Route path="/login" element={<div>login-page</div>} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByText('change-password-page')).toBeInTheDocument()
  })
})
