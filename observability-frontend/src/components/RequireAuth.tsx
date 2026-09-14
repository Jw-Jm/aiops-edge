import React from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '../store/authStore'

const RequireAuth: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const token = useAuthStore((state) => state.token)
  const mustChangePassword = useAuthStore((state) => state.mustChangePassword)
  const location = useLocation()
  if (!token) {
    // PF-PAGE-016: 携带 ?redirect= 参数，登录成功后跳回原深链页面。
    const from = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?redirect=${from}`} state={{ from: location }} replace />
  }
  if (mustChangePassword && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />
  }
  return <>{children}</>
}

export default RequireAuth
