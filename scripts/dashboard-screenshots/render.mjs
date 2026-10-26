import { readFile, access } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const here = dirname(fileURLToPath(import.meta.url))
let browser
let stage = 'configuration'
try {
  const config = JSON.parse(await readFile(process.argv[2], 'utf8'))
  const address = new URL(config.url)
  if (address.protocol !== 'http:' || address.hostname !== '127.0.0.1' || address.search || address.username || address.password) throw Error()
  const ticket = new URLSearchParams(address.hash.slice(1)).get('connect')
  if (!ticket || !/^[a-f0-9]{64}$/.test(ticket)) throw Error()
  const secrets = [ticket, config.endpointToken]
  const transcript = await readFile(config.transcript)
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
  await page.locator('#connection').filter({ hasText: 'Connected' }).waitFor({ timeout: 20000 })
  await page.locator('#monitor').filter({ hasText: 'RAM remaining' }).waitFor({ timeout: 20000 })
  // A native CPU rate needs more than one sample; retain a short real history.
  await page.waitForTimeout(5000)
  async function ensureSafeScreen() {
    if (page.url().includes('connect=')) throw Error()
    const text = await page.locator('body').innerText()
    if (secrets.some(secret => secret && text.includes(secret))) throw Error()
  }
  stage = 'web Monitor'
  await ensureSafeScreen()
  await page.screenshot({ path: join(config.output, 'web-monitor.png'), animations: 'disabled' })
  stage = 'web Resources'
  await page.getByRole('button', { name: '02 Resources', exact: true }).click()
  await page.locator('#resources').filter({ hasText: 'documentation-builds' }).waitFor({ timeout: 15000 })
  await ensureSafeScreen()
  await page.screenshot({ path: join(config.output, 'web-resources.png'), fullPage: true, animations: 'disabled' })
  await context.close()

  stage = 'terminal renderer'
  const terminalContext = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 })
  const terminalPage = await terminalContext.newPage()
  await terminalPage.setContent('<!doctype html><html lang="en"><head><meta charset="utf-8"><title>ARM terminal dashboard</title></head><body style="margin:0;background:#101519"><div id="terminal" style="display:inline-block;padding:16px" aria-label="Actual ARM terminal dashboard"></div></body></html>')
  await terminalPage.addStyleTag({ path: join(here, 'node_modules/@xterm/xterm/css/xterm.css') })
  await terminalPage.addScriptTag({ path: join(here, 'node_modules/@xterm/xterm/lib/xterm.js') })
  await terminalPage.evaluate(async encoded => {
    const terminal = new Terminal({ cols: 140, rows: 60, fontFamily: '"DejaVu Sans Mono", monospace', fontSize: 14,
                                    // Standard dark terminal defaults match ARM's
                                    // canvas/text in internal/tui/theme.go. ANSI
                                    // reset cells must not inherit browser black.
                                    theme: { background: '#101519', foreground: '#e1e9ed' },
                                    scrollback: 0, cursorBlink: false, allowTransparency: false })
    terminal.open(document.getElementById('terminal'))
    const bytes = Uint8Array.from(atob(encoded), value => value.charCodeAt(0))
    await new Promise(resolve => terminal.write(bytes, resolve))
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
    const lines = Array.from({ length: terminal.rows }, (_, i) => terminal.buffer.active.getLine(i)?.translateToString(true) ?? '').join('\n')
    if (!lines.includes('To lease') || !lines.includes('documentation-builds')) throw Error()
    window.terminalScreenText = lines
  }, transcript.toString('base64'))
  const screen = await terminalPage.evaluate(() => window.terminalScreenText)
  if (secrets.some(secret => secret && screen.includes(secret))) throw Error()
  stage = 'terminal PNG'
  await terminalPage.locator('#terminal').screenshot({ path: join(config.output, 'terminal.png'), animations: 'disabled' })
  for (const file of ['web-monitor.png', 'web-resources.png', 'terminal.png']) await access(join(config.output, file))
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
