import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

export const pages = [
  { path: '', heading: 'Builds, tests and analysis compete for resources.' },
  { path: 'start/', heading: 'Install ARM' },
  { path: 'capabilities/', heading: 'Resources and operations' },
  { path: 'docs/', heading: 'Documentation' },
]

// Check the actual publish directory, after Semblance has rendered it.
export function verifyPrerenderedSite(dist, base = process.env.WEBSITE_BASE_PATH ?? '/') {
  const css = readFileSync(join(dist, 'semblance.css'), 'utf8')
  assert(css.length > 1000, 'The published Semblance stylesheet is absent or empty')
  const documents = pages.map(page => {
    const file = page.path + 'index.html'
    const html = readFileSync(join(dist, file), 'utf8')
    assert.match(html, /<div id="app"[^>]* data-sem-ssr>/, `${file}: missing rendered mount`)
    assert(html.includes(`rel="canonical" href="https://getarm.org/${page.path}"`), `${file}: incorrect canonical address`)
    assert(html.includes(page.heading), `${file}: missing page content`)
    assert(html.includes(`<base href="${base}"`), `${file}: incorrect asset base`)
    assert(html.includes(`href="${base}semblance.css" data-sem-styles`), `${file}: missing published styles`)
    assert.match(html, /<meta name="go-import" content="getarm.org git https:\/\/github.com\/inferal-oss\/arm"/)
    assert(!html.includes('Loading Agent Resource Manager.'), `${file}: loading placeholder published`)
    for (const target of pages) {
      assert(html.includes(`formaction="${base}${target.path}"`), `${file}: missing script-free navigation to ${target.path}`)
    }
    return { file, html }
  })
  for (const file of ['cmd/index.html', 'cmd/arm/index.html']) {
    assert.match(readFileSync(join(dist, file), 'utf8'), /name="go-import" content="getarm.org git https:\/\/github.com\/inferal-oss\/arm"/)
  }
  return { documents, css }
}
