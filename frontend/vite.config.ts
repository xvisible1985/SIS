/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/setupTests.ts'],
  },
  server: {
    proxy: {
      '/auth': { target: 'http://localhost:8080', changeOrigin: true },
      '/signals': { target: 'http://localhost:8080', changeOrigin: true },
      '/webhooks': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8080', ws: true, changeOrigin: true },
    },
  },
})
