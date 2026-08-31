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

/** authStore：只保存当前页面生命周期的非持久化会话投影。
 * 认证凭据由 Query API 通过 HttpOnly Cookie 管理；localStorage 不再保存
 * token、role、tenant 或 cluster 等安全相关状态。
 */
export const useAuthStore = create<AuthState>()((set) => ({
  token: '',
  username: '',
  role: '',
  displayName: '',
  mustChangePassword: false,
  login: (token, username = '', role = '', displayName = '', mustChangePassword = false) => {
    set({ token, username, role, displayName, mustChangePassword })
  },
  logout: () => {
    set({ token: '', username: '', role: '', displayName: '', mustChangePassword: false })
  },
}))
