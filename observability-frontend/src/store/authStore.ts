import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface AuthState {
  token: string
  username: string
  role: string
  displayName: string
  login: (token: string, username?: string, role?: string, displayName?: string) => void
  logout: () => void
}

/** authStore：登录态集中管理。内部与 localStorage 同步（兼容现有 client.ts / App.tsx 读取）。 */
export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: localStorage.getItem('token') || '',
      username: localStorage.getItem('username') || '',
      role: localStorage.getItem('role') || '',
      displayName: localStorage.getItem('display_name') || '',
      login: (token, username = '', role = '', displayName = '') => {
        localStorage.setItem('token', token)
        if (username) localStorage.setItem('username', username)
        if (role) localStorage.setItem('role', role)
        if (displayName) localStorage.setItem('display_name', displayName)
        set({ token, username, role, displayName })
      },
      logout: () => {
        localStorage.removeItem('token')
        localStorage.removeItem('username')
        localStorage.removeItem('role')
        localStorage.removeItem('display_name')
        set({ token: '', username: '', role: '', displayName: '' })
      },
    }),
    { name: 'aiops-auth', partialize: (s) => ({ token: s.token, username: s.username, role: s.role, displayName: s.displayName }) },
  ),
)
