import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { pages } from './prerender-integrity.mjs'

// Each new static address describes itself rather than canonizing every section
// as the landing page. Keep the production origin on repository-prefix previews.
for (const page of pages) {
  const file = join('dist', page.path, 'index.html')
  const html = readFileSync(file, 'utf8')
  const canonical = '<link rel="canonical" href="https://getarm.org/" />'
  if (html.split(canonical).length !== 2) throw Error('Unexpected canonical template')
  writeFileSync(file, html.replace(canonical, `<link rel="canonical" href="https://getarm.org/${page.path}" />`))
}
