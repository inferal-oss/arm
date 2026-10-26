import { defineConfig } from 'vite'

export default defineConfig({
  base: './',
  optimizeDeps: {
    exclude: ['@inferal-oss/semblance', 'oxigraph'],
    include: ['@inferal-oss/semblance > n3'],
  },
})
