import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface UIState {
  collapsed: boolean
  darkMode: boolean
  commandOpen: boolean
  activeCommand: string
  toggleCollapsed: () => void
  setDarkMode: (v: boolean) => void
  setCommandOpen: (v: boolean) => void
  setActiveCommand: (v: string) => void
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      collapsed: false,
      darkMode: localStorage.getItem('darkMode') !== 'false',
      commandOpen: false,
      activeCommand: '',
      toggleCollapsed: () => set((s) => ({ collapsed: !s.collapsed })),
      setDarkMode: (v) => {
        set({ darkMode: v })
        localStorage.setItem('darkMode', String(v))
        document.body.classList.toggle('light', !v)
      },
      setCommandOpen: (v) => set({ commandOpen: v }),
      setActiveCommand: (v) => set({ activeCommand: v }),
    }),
    { name: 'aiops-ui' },
  ),
)
