import { readFile, access } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'
import { terminalGeometry } from './geometry.mjs'

const here = dirname(fileURLToPath(import.meta.url))

async function fitContentViewport(page) {
  const footer = page.locator('footer[data-sem-c="status-bar"][aria-label="ARM"]')
  await footer.waitFor({ timeout: 15000 })
  // A full-page screenshot keeps a fixed footer at the original viewport's
  // bottom, where it obscures content halfway through a taller image. Grow
  // the real viewport instead; leave all application styles and data intact.
  for (let attempt = 0; attempt <= 4; attempt++) {
    const frame = await footer.evaluate(bar => {
      const doc = bar.ownerDocument
      const view = doc.defaultView
      const bounds = bar.getBoundingClientRect()
      let contentBottom = 0
      for (const element of doc.body.querySelectorAll('*')) {
        if (element === bar || bar.contains(element) || element.contains(bar) || !element.getClientRects().length) continue
        contentBottom = Math.max(contentBottom, element.getBoundingClientRect().bottom)
      }
      return { width: view.innerWidth, height: view.innerHeight,
        documentHeight: Math.max(doc.documentElement.scrollHeight, doc.body.scrollHeight),
        documentWidth: Math.max(doc.documentElement.scrollWidth, doc.body.scrollWidth),
        contentBottom, footerTop: bounds.top, footerBottom: bounds.bottom, footerHeight: bounds.height }
    })
    if (Object.values(frame).some(value => !Number.isFinite(value)) || frame.width !== 1440 ||
        frame.height < 1000 || frame.height > 3600 || frame.documentWidth > frame.width + 1 || frame.footerHeight <= 0) throw Error()
    // The stock shell's viewport minimum plus inset leaves empty scroll
    // padding. Check actual content bounds instead of growing for that padding.
    if (frame.footerTop >= frame.contentBottom - 1 && frame.footerBottom >= frame.height - 1 &&
        frame.footerBottom <= frame.height + 1) return
    const height = Math.ceil(Math.max(frame.documentHeight + 16, frame.contentBottom + frame.footerHeight + 16, frame.height + 64))
    if (attempt === 4 || height > 3600) throw Error()
    await page.setViewportSize({ width: 1440, height })
    // Two actual layout frames, with a bounded wait if the page stops rendering.
    await page.evaluate(() => new Promise((resolve, reject) => {
      const deadline = setTimeout(() => reject(Error()), 1000)
      requestAnimationFrame(() => requestAnimationFrame(() => { clearTimeout(deadline); resolve() }))
    }))
  }
  throw Error()
}

let browser
let stage = 'configuration'
try {
  const config = JSON.parse(await readFile(process.argv[2], 'utf8'))
  const grid = terminalGeometry(config.terminalGeometry)
  const address = new URL(config.url)
  if (address.protocol !== 'http:' || address.hostname !== '127.0.0.1' || address.search || address.username || address.password) throw Error()
  const ticket = new URLSearchParams(address.hash.slice(1)).get('connect')
  if (!ticket || !/^[a-f0-9]{64}$/.test(ticket)) throw Error()
  const secrets = [ticket, config.endpointToken]
  stage = 'launch Chromium'
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1,
                                            locale: 'en-US', colorScheme: 'dark', serviceWorkers: 'block' })
  // The capture is self-contained: no external network or connected account data.
  await context.route('**/*', route => {
    const request = new URL(route.request().url())
    return request.origin === address.origin ? route.continue() : route.abort()
  })
  const page = await context.newPage()
  stage = 'web authentication'
  await page.goto(config.url, { waitUntil: 'domcontentloaded' })
  // Follow stock Semblance accessibility contracts rather than legacy DOM IDs.
  await page.getByRole('toolbar', { name: 'Backend status', exact: true })
    .getByText('Connected', { exact: true }).waitFor({ timeout: 20000 })
  await page.getByRole('heading', { name: 'Host monitor', exact: true }).waitFor({ timeout: 20000 })
  await page.getByText('RAM remaining', { exact: true }).waitFor({ timeout: 20000 })
  // A native CPU rate needs more than one sample; retain a short real history.
  await page.waitForTimeout(5000)
  async function ensureSafeScreen() {
    if (page.url().includes('connect=')) throw Error()
    const text = await page.locator('body').innerText()
    if (secrets.some(secret => secret && text.includes(secret))) throw Error()
  }
  for (const view of config.views.filter(view => view.surface === 'web')) {
    stage = 'web ' + view.view
    await page.setViewportSize({ width: 1440, height: 1000 })
    stage = 'web ' + view.view + ' navigation'
    await page.getByRole('group', { name: 'Views', exact: true })
      .getByRole('button', { name: view.view, exact: true }).click()
    stage = 'web ' + view.view + ' heading'
    await page.getByRole('heading', { name: view.heading, exact: true }).waitFor({ timeout: 15000 })
    stage = 'web ' + view.view + ' content'
    for (const label of view.labels) await page.getByText(label, { exact: true }).filter({ visible: true }).first().waitFor({ timeout: 15000 })
    if (view.view === 'Activity') {
      const events = page.getByRole('combobox', { name: 'Inspect an event', exact: true })
      await events.waitFor({ timeout: 15000 })
      await events.locator('option').nth(1).waitFor({ state: 'attached', timeout: 15000 })
    }
    stage = 'web ' + view.view + ' viewport'
    await fitContentViewport(page)
    stage = 'web ' + view.view + ' connection'
    await page.getByRole('toolbar', { name: 'Backend status', exact: true })
      .getByText('Connected', { exact: true }).waitFor({ timeout: 15000 })
    await ensureSafeScreen()
    stage = 'web ' + view.view + ' PNG'
    await page.screenshot({ path: join(config.output, view.file), fullPage: false, animations: 'disabled' })
  }
  await context.close()

  stage = 'terminal renderer'
  const terminalContext = await browser.newContext({ viewport: grid.viewport, deviceScaleFactor: 1 })
  const terminalPage = await terminalContext.newPage()
  await terminalPage.setContent('<!doctype html><html lang="en"><head><meta charset="utf-8"><title>ARM terminal dashboard</title></head><body style="margin:0;background:#101519"><div id="terminal" style="display:inline-block;padding:16px" aria-label="Actual ARM terminal dashboard"></div></body></html>')
  stage = 'terminal styles'
  await terminalPage.addStyleTag({ path: join(here, 'node_modules/@xterm/xterm/css/xterm.css') })
  stage = 'terminal library'
  await terminalPage.addScriptTag({ path: join(here, 'node_modules/@xterm/xterm/lib/xterm.js') })
  for (const view of config.views.filter(view => view.surface === 'terminal')) {
    const transcript = await readFile(config.transcripts[view.file])
    stage = 'terminal initialization'
    await terminalPage.evaluate(grid => {
      window.captureTerminal?.dispose()
      document.getElementById('terminal').replaceChildren()
      const terminal = new Terminal({ cols: grid.cols, rows: grid.rows, fontFamily: '"DejaVu Sans Mono", monospace', fontSize: 14,
        theme: { background: '#101519', foreground: '#e1e9ed' }, scrollback: 0, cursorBlink: false, allowTransparency: false })
      terminal.open(document.getElementById('terminal'))
      window.captureTerminal = terminal
    }, { cols: grid.cols, rows: grid.rows })
    stage = 'terminal byte replay'
    await terminalPage.evaluate(async encoded => {
      const terminal = window.captureTerminal
      const bytes = Uint8Array.from(atob(encoded), value => value.charCodeAt(0))
      await new Promise(resolve => terminal.write(bytes, resolve))
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
      window.terminalScreenText = Array.from({ length: terminal.rows }, (_, i) =>
        terminal.buffer.active.getLine(i)?.translateToString(true) ?? '').join('\n')
    }, transcript.toString('base64'))
    stage = 'terminal label verification'
    const visible = await terminalPage.evaluate(labels => ({
      labels: labels.every(label => window.terminalScreenText.includes(label)),
      alternate: window.captureTerminal.buffer.active.type === 'alternate',
      cols: window.captureTerminal.cols, rows: window.captureTerminal.rows,
    }), view.labels)
    if (visible.cols !== grid.cols || visible.rows !== grid.rows || !visible.alternate || !visible.labels) throw Error()
    stage = 'terminal credential exclusion'
    const screen = await terminalPage.evaluate(() => window.terminalScreenText)
    if (secrets.some(secret => secret && screen.includes(secret))) throw Error()
    stage = 'terminal PNG'
    await terminalPage.locator('#terminal').screenshot({ path: join(config.output, view.file), animations: 'disabled' })
  }
  for (const view of config.views) await access(join(config.output, view.file))
  await terminalContext.close()
  await browser.close()
  browser = undefined
  console.log('Dashboard images rendered from live web and PTY output.')
} catch {
  // Playwright exceptions can include the one-use URL: never print the raw error.
  console.error('Dashboard renderer failed at safe stage: ' + stage)
  process.exitCode = 1
} finally {
  await browser?.close()
}
