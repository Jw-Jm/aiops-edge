import { create } from 'zustand'

interface AuthState {
  token: string
  username: string
  role: string
  displayName: string
  mustChangePassword: boolean
  login: (token: string, username?: string, role?: string, displayName?: string, mustChangePassword?: boolean) => void
  logout: () => void
}

/** authStore：登录态集中管理。token/用户名/角色仅存一份 localStorage（client.ts 的 axios 拦截器为唯一读取来源）。 */
export const useAuthStore = create<AuthState>()((set) => ({
  token: localStorage.getItem('token') || '',
  username: localStorage.getItem('username') || '',
  role: localStorage.getItem('role') || '',
  displayName: localStorage.getItem('display_name') || '',
  mustChangePassword: localStorage.getItem('must_change_password') === 'true',
  login: (token, username = '', role = '', displayName = '', mustChangePassword = false) => {
    localStorage.setItem('token', token)
    if (username) localStorage.setItem('username', username)
    if (role) localStorage.setItem('role', role)
    if (displayName) localStorage.setItem('display_name', displayName)
    localStorage.setItem('must_change_password', String(mustChangePassword))
    set({ token, username, role, displayName, mustChangePassword })
  },
  logout: () => {
    localStorage.removeItem('token')
    localStorage.removeItem('username')
    localStorage.removeItem('role')
    localStorage.removeItem('display_name')
    localStorage.removeItem('must_change_password')
    set({ token: '', username: '', role: '', displayName: '', mustChangePassword: false })
  },
}))
