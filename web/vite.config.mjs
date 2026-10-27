import { defineConfig } from 'vite';
import { resolve } from 'node:path';
import {embeddedNotices} from './license-notices.mjs';
export default defineConfig({
  root: resolve(import.meta.dirname, '../internal/server/web/semblance'),
  base: '/assets/',
  resolve: {alias: [
    {find: /^oxigraph\/web\.js$/, replacement: resolve(import.meta.dirname, 'arm-wat-only-store.mjs')},
    {find: '@inferal-oss/semblance/web', replacement: resolve(import.meta.dirname, 'third_party/semblance/src/web/index.ts')},
    {find: '@inferal-oss/semblance', replacement: resolve(import.meta.dirname, 'third_party/semblance/src/index.ts')},
  ]},
  optimizeDeps: {exclude: ['oxigraph'], include: ['n3']},
  plugins:[{name:'embedded-license-notices',generateBundle(){this.emitFile({type:'asset',fileName:'LICENSES.txt',source:embeddedNotices()});}}],
  build: {
    outDir: resolve(import.meta.dirname, '../internal/server/web/assets'),
    emptyOutDir: true, target: 'es2023', assetsInlineLimit: 0,
    rollupOptions: {input: resolve(import.meta.dirname, '../internal/server/web/semblance/main.mjs'), external: ['/novnc/core/rfb.js','/ard/ard-patch.js'],
      output: {entryFileNames: 'arm.mjs', chunkFileNames: '[name]-[hash].mjs'}},
  },
});
