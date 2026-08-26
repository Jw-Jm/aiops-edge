import React from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '../store/authStore'

const RequireAuth: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const token = localStorage.getItem('token')
  const mustChangePassword = useAuthStore((state) => state.mustChangePassword)
  const location = useLocation()
  if (!token) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }
  if (mustChangePassword && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />
  }
  return <>{children}</>
}

export default RequireAuth
