/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 4001,
    strictPort: true,
    host: true,
    allowedHosts: true,
    proxy: {
      '/auth': { target: 'http://localhost:8081', changeOrigin: true },
      '/signals': { target: 'http://localhost:8081', changeOrigin: true },
      '/webhooks': { target: 'http://localhost:8081', changeOrigin: true },
      '/accounts': { target: 'http://localhost:8081', changeOrigin: true },
      '/strategies': { target: 'http://localhost:8081', changeOrigin: true },
      '/strategy-templates': { target: 'http://localhost:8081', changeOrigin: true },
      '/trader': { target: 'http://localhost:8081', changeOrigin: true },
      '^/admin/': { target: 'http://localhost:8081', changeOrigin: true },
      '/signal-types': { target: 'http://localhost:8081', changeOrigin: true },
      '/indicator-types': { target: 'http://localhost:8081', changeOrigin: true },
      '/bots': { target: 'http://localhost:8081', changeOrigin: true },
      '/trade-history': { target: 'http://localhost:8081', changeOrigin: true },
      '/dashboard': { target: 'http://localhost:8081', changeOrigin: true },
      '/instrument-info': { target: 'http://localhost:8081', changeOrigin: true },
      '/account': { target: 'http://localhost:8081', changeOrigin: true },
      '/coin-icon': { target: 'http://localhost:8081', changeOrigin: true },
      '/payments': { target: 'http://localhost:8081', changeOrigin: true },
      '/positions': { target: 'http://localhost:8081', changeOrigin: true },
      '/events': { target: 'http://localhost:8081', changeOrigin: true },
      '/strategy-defaults': { target: 'http://localhost:8081', changeOrigin: true },
      '/coin-filter': { target: 'http://localhost:8081', changeOrigin: true },
      '/bybit-news': { target: 'http://localhost:8081', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8081', ws: true, changeOrigin: true },
    },
  },
})
