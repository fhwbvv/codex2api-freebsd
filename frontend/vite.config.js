import path from 'path'
import { execSync } from 'child_process'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

function getVersion() {
  if (process.env.VITE_APP_VERSION) return process.env.VITE_APP_VERSION
  try {
    return execSync('git describe --tags --abbrev=0', { encoding: 'utf-8' }).trim()
  } catch {
    return 'dev'
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: '/admin/',
  define: {
    __APP_VERSION__: JSON.stringify(getVersion()),
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1000,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (/[\\/]node_modules[\\/](react|react-dom|react-router|react-router-dom|scheduler|clsx|tailwind-merge|class-variance-authority)[\\/]/.test(id)) {
            return 'react-vendor'
          }
          if (/[\\/]node_modules[\\/](i18next|react-i18next)[\\/]/.test(id)) {
            return 'i18n-vendor'
          }
          if (/[\\/]node_modules[\\/](recharts|d3-[^/\\]+|victory-vendor|internmap)[\\/]/.test(id)) {
            return 'recharts-vendor'
          }
          if (/[\\/]node_modules[\\/](radix-ui|@radix-ui)[\\/]/.test(id)) {
            return 'radix-vendor'
          }
          if (/[\\/]node_modules[\\/]lucide-react[\\/]/.test(id)) {
            return 'lucide-vendor'
          }
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080'
    }
  }
})
