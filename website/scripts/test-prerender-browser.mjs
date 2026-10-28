import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdirSync, readFileSync, statSync } from 'node:fs'
import { resolve, sep, extname } from 'node:path'
import { chromium } from '../../scripts/dashboard-screenshots/node_modules/playwright/index.mjs'
import { pages, verifyPrerenderedSite } from './prerender-integrity.mjs'

const dist = resolve(process.argv[2] ?? 'dist')
const base = process.env.WEBSITE_BASE_PATH ?? '/'
verifyPrerenderedSite(dist, base)
const mime = { '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript', '.wasm': 'application/wasm', '.svg': 'image/svg+xml', '.png': 'image/png' }
const server = createServer((req, res) => {
  try {
    const path = new URL(req.url, 'http://localhost').pathname
    if (!path.startsWith(base)) throw Error('Outside base')
    let file = resolve(dist, decodeURIComponent(path.slice(base.length)))
    if (file !== dist && !file.startsWith(dist + sep)) throw Error('Outside dist')
    if (statSync(file).isDirectory()) file += '/index.html'
    res.setHeader('content-type', mime[extname(file)] ?? 'application/octet-stream')
    res.end(readFileSync(file))
  } catch {
    res.writeHead(404).end()
  }
})
const browser = await chromium.launch({ headless: true })
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const origin = `http://127.0.0.1:${server.address().port}`
try {
  for (const width of [1440, 390]) {
    const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width, height: 900 } })
    for (const page of pages) {
      const tab = await context.newPage()
      await tab.goto(origin + base + page.path)
      await tab.getByRole('heading', { name: page.heading, exact: true }).waitFor({ state: 'visible' })
      assert(await tab.locator('link[data-sem-styles]').evaluate(node => !!node.sheet), 'Published CSS must load without scripts')
      assert(await tab.locator('body').evaluate(node => node.scrollWidth <= innerWidth + 1), 'Page must fit the viewport')
      if (process.env.WEBSITE_SCREENSHOT_DIR) {
        mkdirSync(process.env.WEBSITE_SCREENSHOT_DIR, { recursive: true })
        await tab.screenshot({ path: resolve(process.env.WEBSITE_SCREENSHOT_DIR, `${width}-${page.path.replaceAll('/', '') || 'overview'}.png`), fullPage: true })
      }
      if (width === 1440) {
        await tab.getByRole('button', { name: 'Get started', exact: true }).click()
        assert.equal(new URL(tab.url()).pathname, base + 'start/')
        await tab.getByRole('heading', { name: 'Install ARM', exact: true }).waitFor()
      }
      await tab.close()
    }
    await context.close()
  }
  console.log('PASS: four styled pages at desktop/phone widths without JavaScript; native desktop navigation')

  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const tab = await context.newPage()
  const errors = []
  const observe = tab => {
    tab.on('pageerror', error => errors.push(error.message))
    tab.on('console', message => {
      if (message.type() === 'error' && message.text().includes('ARM website initialization failed')) errors.push(message.text())
    })
  }
  observe(tab)
  let release
  const gate = new Promise(resolve => { release = resolve })
  await tab.route('**/*.wasm', async route => { await gate; await route.continue() })
  await tab.goto(origin + base, { waitUntil: 'domcontentloaded' })
  const heading = tab.getByRole('heading', { name: pages[0].heading, exact: true })
  await heading.waitFor()
  await tab.getByRole('button', { name: 'Get started', exact: true }).click()
  // The capture script holds this interaction while the engine is still loading.
  assert.equal(new URL(tab.url()).pathname, base)
  release()
  await tab.locator('#app:not([data-sem-ssr])').waitFor({ timeout: 30000 })
  await tab.getByRole('heading', { name: 'Install ARM', exact: true }).waitFor({ timeout: 30000 })
  assert.equal(new URL(tab.url()).pathname, base + 'start/')
  await tab.getByRole('button', { name: 'Overview', exact: true }).click()
  await heading.waitFor()
  // Check adoption separately without a queued navigation changing the page.
  await tab.close()
  const adopted = await context.newPage()
  observe(adopted)
  let releaseAdopt
  const adoptGate = new Promise(resolve => { releaseAdopt = resolve })
  await adopted.route('**/*.wasm', async route => { await adoptGate; await route.continue() })
  await adopted.goto(origin + base, { waitUntil: 'domcontentloaded' })
  const node = await adopted.getByRole('heading', { name: pages[0].heading, exact: true }).elementHandle()
  releaseAdopt()
  await adopted.locator('#app:not([data-sem-ssr])').waitFor({ timeout: 30000 })
  assert(await node.evaluate(el => el.isConnected), 'Takeover must preserve the rendered heading node')
  assert.equal(await adopted.evaluate(() => document.adoptedStyleSheets.length), 0, 'Published CSS must be reused')
  for (const page of pages.slice(1)) {
    await adopted.goto(origin + base + page.path)
    await adopted.locator('#app:not([data-sem-ssr])').waitFor({ timeout: 30000 })
    await adopted.getByRole('heading', { name: page.heading, exact: true }).waitFor()
  }
  for (const page of pages) {
    const fragment = page.path.replace('/', '') || 'overview'
    await adopted.goto(origin + base + '#' + fragment)
    await adopted.locator('#app:not([data-sem-ssr])').waitFor({ timeout: 30000 })
    await adopted.getByRole('heading', { name: page.heading, exact: true }).waitFor()
    assert.equal(new URL(adopted.url()).pathname, base + page.path)
  }
  await adopted.getByRole('button', { name: 'Get started', exact: true }).click()
  await adopted.getByRole('heading', { name: 'Install ARM', exact: true }).waitFor()
  await adopted.getByRole('button', { name: 'Capabilities', exact: true }).click()
  await adopted.getByRole('heading', { name: 'Resources and operations', exact: true }).waitFor()
  await adopted.goBack()
  await adopted.getByRole('heading', { name: 'Install ARM', exact: true }).waitFor()
  await adopted.goForward()
  await adopted.getByRole('heading', { name: 'Resources and operations', exact: true }).waitFor()
  await adopted.getByRole('button', { name: 'Overview', exact: true }).click()
  const opener = adopted.locator('button.sem-cell').first()
  await opener.click()
  await adopted.getByRole('dialog', { name: 'ARM dashboard screenshots', exact: true }).waitFor()
  await adopted.keyboard.press('Escape')
  await adopted.getByRole('dialog', { name: 'ARM dashboard screenshots', exact: true }).waitFor({ state: 'hidden' })
  assert(await opener.evaluate(el => el === el.ownerDocument.activeElement), 'Closing the gallery must return focus to its opener')
  await adopted.setViewportSize({ width: 390, height: 900 })
  const picker = adopted.getByRole('combobox', { name: 'Site navigation', exact: true })
  await picker.selectOption({ label: 'Docs & source' })
  await adopted.getByRole('heading', { name: 'Documentation', exact: true }).waitFor()
  assert.equal(await picker.evaluate(el => el.selectedOptions[0].textContent), 'Docs & source')
  await adopted.getByRole('button', { name: 'Display', exact: true }).click()
  await adopted.getByRole('group', { name: 'Color scheme', exact: true }).getByRole('button', { name: /Day$/ }).click()
  await adopted.locator('#app [data-sem-color-scheme="light"]').waitFor()
  assert.deepEqual(errors, [])
  await context.close()
  console.log('PASS: DOM/style reuse, queued pre-takeover click, every direct page and legacy fragment, back/forward, gallery focus, compact picker, display')
} finally {
  await browser.close()
  await new Promise(resolve => server.close(resolve))
  console.log('PASS: original browser and HTTP fixture closed')
}
