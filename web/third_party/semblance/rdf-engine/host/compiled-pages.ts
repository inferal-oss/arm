// Whole-page timings on an engine store, interpreted or compiled:
//   SEMBLANCE_ENGINE_COMPILE=<mode> npx tsx rdf-engine/host/compiled-pages.ts [rust|wat] [pages|warehouse|grid]
// mode: 0 interpreted; n: each prepared query compiled on its n-th run; idle: in idle time
// (Engine.compileIdle). After the mount and after every visit or interaction the host would
// be idle: the store's `maintain` gets 8 ms slices until it has nothing left (at most 50 ms),
// as demo/main.ts gives it the idle time warming leaves; that time is reported apart (idleMs).
// - pages: mount the shell (the Playground), then show Home, Docs, Components, Examples and
//   the Playground three times: each page's first and best visit.
// - warehouse: the site, the Warehouse example, then 30 movements (sell or restock, alternating
//   items): the first 10 and the last 20.
// - grid: the network inspector over 500 captured requests, sorted by duration, then 40
//   responses arriving: the first 10 and the last 30.
// Prints one JSON line: the timings, the time spent in queries (queryMs: all of it, the
// mount's too) and the compile counters.
import '../../src/behaviour/events.ts'
import { NETWORK_GRAPH, NS } from '../../src/ns.ts'
import { mountShell, mountSite } from '../../test/harness.ts'
import { type EngineStore, engineStoreHooks } from './store.ts'

const kind = (process.argv[2] ?? 'rust') as 'rust' | 'wat'
const scenario = (process.argv[3] ?? 'pages') as 'pages' | 'warehouse' | 'grid'
const UI = 'https://semblance.dev/demo#'
let store: EngineStore | undefined
/** Milliseconds in the store's SPARQL calls (all of the engine's query time, either way). */
let queryMs = 0
engineStoreHooks.created = (s) => {
  store = s
  for (const m of ['select', 'construct', 'constructFor'] as const) {
    const f = s[m] as (...a: unknown[]) => unknown
    ;(s as unknown as Record<string, unknown>)[m] = (...a: unknown[]) => {
      const t = performance.now()
      try {
        return f(...a)
      } finally {
        queryMs += performance.now() - t
      }
    }
  }
}

let idleMs = 0
/** What the host's idle callbacks would give the store. */
function idle() {
  const t = performance.now()
  for (let i = 0; i < 6 && store!.maintain!(8); i++);
  idleMs += performance.now() - t
}
/** Query milliseconds of each timed step, in order (the mount's first). */
const stepQueryMs: number[] = []
const time = (f: () => void) => {
  const t = performance.now()
  const q = queryMs
  f()
  const ms = performance.now() - t
  stepQueryMs.push(queryMs - q)
  idle()
  return ms
}
const sum = (xs: number[]) => xs.reduce((a, b) => a + b, 0)
const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[xs.length >> 1]!

let mountMs = 0
let result: Record<string, unknown> = {}
if (scenario === 'pages') {
  let shell!: ReturnType<typeof mountShell>
  mountMs = time(() => (shell = mountShell(5, kind, 'sparql')))
  const pages = ['Home', 'Docs', 'Components', 'Examples', 'Playground'] as const
  const visits: Record<string, number[]> = {}
  for (let round = 0; round < 3; round++) for (const page of pages) (visits[page] ??= []).push(time(() => shell.showPage(page)))
  result = {
    pages: Object.fromEntries(Object.entries(visits).map(([p, v]) => [p, { first: v[0], best: Math.min(...v.slice(1)) }])),
    totalFirstMs: sum(pages.map((p) => visits[p]![0]!)),
    totalBestMs: sum(pages.map((p) => Math.min(...visits[p]!.slice(1)))),
    // of a revisit round (the mean of rounds 2 and 3): the time in queries
    revisitQueryMs: sum(stepQueryMs.slice(6)) / 2,
  }
} else if (scenario === 'warehouse') {
  let h!: ReturnType<typeof mountSite>
  mountMs = time(() => (h = mountSite(kind, 'sparql')))
  const visible = (el: Element | null | undefined) => !!el && !el.closest('[hidden]')
  const click = (el: Element) => (h.fire(el, 'click'), h.shell.flush())
  const button = (text: string, within: ParentNode = h.document) => [...within.querySelectorAll('button')].find((b) => b.textContent!.trim() === text && visible(b))!
  const navItem = (text: string) => [...h.document.querySelectorAll('[data-sem-c=nav-list] button')].find((b) => b.textContent!.trim() === text && visible(b))!
  const openMs = time(() => (click(button('Examples')), click(navItem('Warehouse'))))
  const root = () => h.shell.nodeOf(UI + 'ex-warehouse/~root') as Element
  const choose = (item: string) => {
    const select = root().querySelector('[data-sem-c=choice] select')!
    const o = [...select.querySelectorAll('option')].find((x) => x.textContent === item)!
    Object.defineProperty(select, 'value', { value: o.getAttribute('value'), configurable: true })
    h.fire(select, 'change')
    h.shell.flush()
  }
  const items = ['Chain lube', 'Helmet', 'Puncture kit', 'Water bottle', 'Tyre levers']
  const steps: number[] = []
  for (let i = 0; i < 30; i++) steps.push(time(() => (choose(items[i % items.length]!), click(button(i % 2 ? 'Restock' : 'Sell')))))
  result = { openMs, first10Ms: sum(steps.slice(0, 10)), last20Ms: sum(steps.slice(10)), medianLastMs: median(steps.slice(10)), lastQueryMs: sum(stepQueryMs.slice(12)) }
} else {
  let h!: ReturnType<typeof mountShell>
  mountMs = time(() => (h = mountShell(5, kind, 'sparql')))
  const f = h.store.factory
  const nn = (s: string) => f.namedNode(s)
  const g = nn(NETWORK_GRAPH)
  const dbl = (n: number) => f.literal(String(n), nn(NS.xsd + 'double'))
  const int = (n: number) => f.literal(String(n), nn(NS.xsd + 'integer'))
  const entry = (i: number) => nn(`urn:semblance:net:bench-${i}`)
  const NET = NS.net
  const openMs = time(() => {
    h.runtime.transaction(() => {
      const add = []
      for (let i = 1; i <= 500; i++) {
        const e = entry(i)
        add.push(
          f.quad(nn(NET + 'Capture'), nn(NS.tree + 'member'), e, g),
          f.quad(e, nn(NS.rdf + 'type'), nn(NET + 'Exchange'), g),
          f.quad(e, nn(NET + 'kind'), nn(NET + 'HttpKind'), g),
          f.quad(e, nn(NET + 'label'), f.literal(`GET /api/${i}`), g),
          f.quad(e, nn(NET + 'sequence'), int(i), g),
          f.quad(e, nn(NET + 'startedAt'), dbl(1000 + i), g),
          f.quad(e, nn(NET + 'status'), nn(NET + 'Pending'), g),
        )
      }
      h.runtime.write(NETWORK_GRAPH, { add, remove: [] })
    })
    const app = nn(UI + 'app')
    h.runtime.apply({
      remove: [...h.store.match(app, nn(UI + 'page'), null), ...h.store.match(app, nn(UI + 'example'), null)],
      add: [f.quad(app, nn(UI + 'page'), nn(UI + 'Examples')), f.quad(app, nn(UI + 'example'), nn(UI + 'ExNetwork'))],
    })
    h.runtime.apply({ remove: [], add: [f.quad(nn(UI + 'net-grid'), nn(NS.c + 'sortColumn'), nn(UI + 'net-col-took'))] })
    h.shell.flush()
  })
  const steps: number[] = []
  for (let k = 0; k < 40; k++) {
    const i = 1 + ((k * 37) % 500)
    const e = entry(i)
    const r = nn(`urn:semblance:net:bench-${i}/~resp`)
    steps.push(
      time(() => {
        h.runtime.write(NETWORK_GRAPH, {
          remove: [f.quad(e, nn(NET + 'status'), nn(NET + 'Pending'), g)],
          add: [f.quad(e, nn(NET + 'status'), nn(NET + 'Complete'), g), f.quad(e, nn(NET + 'endedAt'), dbl(1000 + i + ((k * 53) % 300)), g), f.quad(e, nn(NS.http + 'resp'), r, g), f.quad(r, nn(NS.http + 'statusCodeValue'), int(200), g)],
        })
        h.shell.flush()
      }),
    )
  }
  result = { openMs, first10Ms: sum(steps.slice(0, 10)), last30Ms: sum(steps.slice(10)), medianLastMs: median(steps.slice(10)), lastQueryMs: sum(stepQueryMs.slice(12)) }
}
const s = store!.engine.stats
if (process.argv.includes('--profiles')) {
  // What compiled in idle time: runs and ms per binding combination each way, the least gain first.
  const rows = store!.engine.idleCompiledProfiles.map((p) => ({
    runs: p.runs,
    combos: p.combos,
    interpreted: p.ms / p.combos,
    compiledRuns: p.compiledRuns,
    compiled: p.compiledRuns > 1 ? p.compiledMs / p.compiledCombos : NaN,
    query: p.query.vars.join(' ') + ' | ' + p.shape,
  }))
  rows.sort((a, b) => b.compiled / b.interpreted - a.compiled / a.interpreted)
  for (const r of rows) console.error(JSON.stringify(r))
}
const mode = process.env.SEMBLANCE_ENGINE_COMPILE ?? '0'
console.log(
  JSON.stringify({
    kind,
    scenario,
    mode,
    mountMs,
    ...result,
    idleMs,
    queryMs,
    compiled: s.compiled,
    idleCompiled: s.idleCompiled,
    reverted: s.reverted,
    fallbacks: s.fallbacks,
    declined: [...s.declined.values()].reduce((a, b) => a + b, 0),
    compileMs: s.emitMs + s.wasmMs,
    compiledRuns: s.compiledRuns,
    interpretedRuns: s.interpretedRuns,
  }),
)
process.exit(0)
