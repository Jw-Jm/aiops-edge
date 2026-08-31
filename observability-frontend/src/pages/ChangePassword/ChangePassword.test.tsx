import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import ChangePassword from './index'

describe('change password page', () => {
  it('requires the current and replacement passwords', () => {
    render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <ChangePassword />
      </MemoryRouter>,
    )

    expect(screen.getByText('首次登录需要修改密码')).toBeInTheDocument()
    expect(screen.getByLabelText('当前密码')).toBeInTheDocument()
    expect(screen.getByLabelText('新密码')).toBeInTheDocument()
    expect(screen.getByLabelText('确认新密码')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '修改密码并继续' })).toBeInTheDocument()
  })
})
