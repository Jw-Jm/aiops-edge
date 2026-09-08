import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface UIState {
  collapsed: boolean
  aiDockOpen: boolean
  toggleCollapsed: () => void
  setAiDockOpen: (v: boolean) => void
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      collapsed: false,
      aiDockOpen: false,
      toggleCollapsed: () => set((s) => ({ collapsed: !s.collapsed })),
      setAiDockOpen: (v) => set({ aiDockOpen: v }),
    }),
    {
      name: 'aiops-ui-v4',
      partialize: (s) => ({
        collapsed: s.collapsed,
        aiDockOpen: s.aiDockOpen,
      }),
    },
  ),
)
