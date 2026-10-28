import { defineConfig } from 'vite'
import { semblance } from '@inferal-oss/semblance/node'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  base: process.env.WEBSITE_BASE_PATH ?? '/',
  plugins: [semblance({ workers: 1 })],
  resolve: {
    alias: [{ find: /^oxigraph\/web\.js$/, replacement: fileURLToPath(new URL('./arm-wat-only-store.mjs', import.meta.url)) }],
  },
  optimizeDeps: {
    exclude: ['@inferal-oss/semblance', 'oxigraph'],
    include: ['@inferal-oss/semblance > n3'],
  },
})
