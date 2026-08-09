import { create } from 'zustand'

interface AuthState {
  token: string
  username: string
  role: string
  displayName: string
  login: (token: string, username?: string, role?: string, displayName?: string) => void
  logout: () => void
}

/** authStore：登录态集中管理。token/用户名/角色仅存一份 localStorage（client.ts 的 axios
 *  拦截器为唯一读取来源），不重复落 zustand persist，避免同一份凭据被多次暴露。 */
export const useAuthStore = create<AuthState>()((set) => ({
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
}))
