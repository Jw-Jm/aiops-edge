import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '../store/authStore'

/** RequireAuth 路由守卫：未登录跳转 /login，登录后渲染子路由。 */
export default function RequireAuth({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token) || localStorage.getItem('token') || ''
  const location = useLocation()
  if (!token) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return <>{children}</>
}
