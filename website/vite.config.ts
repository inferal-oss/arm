import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  base: './',
  resolve: {
    alias: [{ find: /^oxigraph\/web\.js$/, replacement: fileURLToPath(new URL('./arm-wat-only-store.mjs', import.meta.url)) }],
  },
  optimizeDeps: {
    exclude: ['@inferal-oss/semblance', 'oxigraph'],
    include: ['@inferal-oss/semblance > n3'],
  },
})
