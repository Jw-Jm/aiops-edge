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

/** authStore：当前会话的 UI 投影。
 * 认证凭据由 Query API 通过 HttpOnly Cookie 管理；这里只保存非敏感的
 * 会话标记（token 为占位标记，不含真实凭据），并持久化到 sessionStorage
 * 以支持整页刷新/新标签恢复登录态（PF-PAGE-015/016）。登出时清除。
 */
const AUTH_STORAGE_KEY = 'aiops-auth-session'

interface PersistedAuth {
  token: string
  username: string
  role: string
  displayName: string
  mustChangePassword: boolean
}

function readPersistedAuth(): PersistedAuth | null {
  try {
    const raw = sessionStorage.getItem(AUTH_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as PersistedAuth
    if (!parsed?.token) return null
    return parsed
  } catch {
    return null
  }
}

const persisted = readPersistedAuth()

export const useAuthStore = create<AuthState>()((set) => ({
  token: persisted?.token ?? '',
  username: persisted?.username ?? '',
  role: persisted?.role ?? '',
  displayName: persisted?.displayName ?? '',
  mustChangePassword: persisted?.mustChangePassword ?? false,
  login: (token, username = '', role = '', displayName = '', mustChangePassword = false) => {
    const next: PersistedAuth = { token, username, role, displayName, mustChangePassword }
    set(next)
    try {
      sessionStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(next))
    } catch { /* 存储不可用时退化为仅内存会话 */ }
  },
  logout: () => {
    // PF-UI-004: 先吊销服务端会话（端点缺失/404 时静默降级，不阻塞本地登出）。
    // 用原生 fetch 避免 api client ↔ authStore 的循环依赖。
    try {
      fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'include' }).catch(() => {})
    } catch { /* ignore */ }
    set({ token: '', username: '', role: '', displayName: '', mustChangePassword: false })
    try {
      sessionStorage.removeItem(AUTH_STORAGE_KEY)
    } catch { /* ignore */ }
  },
}))
