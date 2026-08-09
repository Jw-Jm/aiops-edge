import { create } from 'zustand'
import api from '../api/client'

export interface ChatSession {
  session_id: string
  title?: string
  updated_at?: string
}

interface ChatState {
  sessions: ChatSession[]
  activeSession: string
  loading: boolean
  loadSessions: () => Promise<void>
  setActiveSession: (sid: string) => void
  removeSession: (sid: string) => Promise<void>
  clear: () => void
}

/** chatStore：会话列表集中管理（后端 /ai/sessions 数据源）。 */
export const useChatStore = create<ChatState>((set, get) => ({
  sessions: [],
  activeSession: '',
  loading: false,
  loadSessions: async () => {
    set({ loading: true })
    try {
      const r = await api.get('/ai/sessions')
      set({ sessions: r.data?.sessions || [] })
    } catch {
      set({ sessions: [] })
    } finally {
      set({ loading: false })
    }
  },
  setActiveSession: (sid) => set({ activeSession: sid }),
  removeSession: async (sid) => {
    try {
      await api.delete(`/ai/session/${sid}`)
      set({ sessions: get().sessions.filter((s) => s.session_id !== sid) })
      if (get().activeSession === sid) set({ activeSession: '' })
    } catch {
      /* 静默 */
    }
  },
  clear: () => set({ sessions: [], activeSession: '', loading: false }),
}))
