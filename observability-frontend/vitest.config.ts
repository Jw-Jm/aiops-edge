import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    environmentOptions: { jsdom: { url: 'http://localhost/' } },
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    clearMocks: true,
    // The Ant Design/jsdom interaction tests contend for the same local CPU
    // and can exceed Vitest's per-test timeout when several workers render
    // large component trees concurrently. Keep the acceptance suite
    // deterministic; this is also faster on the local OrbStack runner.
    minWorkers: 1,
    maxWorkers: 1,
  },
})
