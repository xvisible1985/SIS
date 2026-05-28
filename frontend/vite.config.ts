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
    port: 4000,
    strictPort: true,
    host: true,
    allowedHosts: true,
    proxy: {
      '/auth': { target: 'http://localhost:8080', changeOrigin: true },
      '/signals': { target: 'http://localhost:8080', changeOrigin: true },
      '/webhooks': { target: 'http://localhost:8080', changeOrigin: true },
      '/accounts': { target: 'http://localhost:8080', changeOrigin: true },
      '/strategies': { target: 'http://localhost:8080', changeOrigin: true },
      '/strategy-templates': { target: 'http://localhost:8080', changeOrigin: true },
      '/trader': { target: 'http://localhost:8080', changeOrigin: true },
      '^/admin/': { target: 'http://localhost:8080', changeOrigin: true },
      '/signal-types': { target: 'http://localhost:8080', changeOrigin: true },
      '/indicator-types': { target: 'http://localhost:8080', changeOrigin: true },
      '/bots': { target: 'http://localhost:8080', changeOrigin: true },
      '/trade-history': { target: 'http://localhost:8080', changeOrigin: true },
      '/dashboard': { target: 'http://localhost:8080', changeOrigin: true },
      '/instrument-info': { target: 'http://localhost:8080', changeOrigin: true },
      '/account': { target: 'http://localhost:8080', changeOrigin: true },
      '/coin-icon': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8080', ws: true, changeOrigin: true },
    },
  },
})
