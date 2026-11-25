import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  build: { outDir: '../web/dist', emptyOutDir: true },
  server: {
    proxy: {
      // The dev UI talks to dev.sh's :20140. KINE_BACKEND points it elsewhere.
      '/api': process.env.KINE_BACKEND || 'http://localhost:20140',
      '/v1': process.env.KINE_BACKEND || 'http://localhost:20140',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
  },
})
