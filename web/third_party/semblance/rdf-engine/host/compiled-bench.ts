// Compiled queries against the interpreter, on Semblance's own queries:
//   npx tsx rdf-engine/host/compiled-bench.ts [rust|wat] [--out file.json] [--max-calls n]
//
// 1. Coverage: every query of the corpus (parse-check's semblanceQueries()),
//    prepared with its $variables bound as Semblance binds them, compiled or declined.
// 2. Calls: the shell and the site are rendered on an engine store while every
//    construct / constructFor / select call is recorded (query, bound foci, graphs).
//    Each distinct call is replayed on that store, interpreted and compiled:
//    results compared, and timed (emit, WebAssembly.compile, instantiate, runs).
// 3. Unbound: every compilable corpus query that Semblance runs unbound (no $this,
//    $event) run on the same store, compared. (Rules run unbound can take gigabytes.)
import { readFileSync, writeFileSync } from 'node:fs'
import '../../src/behaviour/events.ts'
import { SPARQL_PREFIXES } from '../../src/ns.ts'
import { mountShell, mountSite } from '../../test/harness.ts'
import { type Engine, Form, Kind, type Query } from './engine.ts'
import { semblanceQueries } from './parse-check.ts'
import { type EngineStore, engineStoreHooks } from './store.ts'

const kind = (process.argv[2] ?? 'rust') as 'rust' | 'wat'
const opt = (name: string) => {
  const i = process.argv.indexOf(name)
  return i < 0 ? undefined : process.argv[i + 1]
}
const maxCalls = Number(opt('--max-calls') ?? 4000)
const profile = process.argv.includes('--profile')

interface Call {
  method: 'construct' | 'constructFor' | 'select'
  text: string
  variable?: string
  values: number[]
  graphs: number[]
}

// --- recording ---------------------------------------------------------------------
const calls: Call[] = []
let recording: EngineStore | undefined
engineStoreHooks.created = (store) => {
  recording = store
  const graphIds = (graphs: unknown[]) => (graphs as Parameters<EngineStore['idOf']>[0][]).map((g) => store.idOf(g)).filter((x) => x)
  const { construct, constructFor, select } = store
  store.construct = (text, graphs) => {
    calls.push({ method: 'construct', text, values: [], graphs: graphIds(graphs) })
    return construct(text, graphs)
  }
  store.select = (text, graphs) => {
    calls.push({ method: 'select', text, values: [], graphs: graphIds(graphs) })
    return select(text, graphs)
  }
  store.constructFor = (text, variable, values, graphs) => {
    calls.push({ method: 'constructFor', text, variable, values: values.map((v) => store.idOf(v)).filter((x) => x), graphs: graphIds(graphs) })
    return constructFor!(text, variable, values, graphs)
  }
}

const t0 = performance.now()
const shell = mountShell(5, kind, 'sparql')
for (const page of ['Home', 'Docs', 'Components', 'Examples', 'Playground'] as const) shell.showPage(page)
const store = recording!
const site = mountSite(kind, 'sparql')
void site
const e: Engine = store.engine
console.error(`${kind}: rendered in ${(performance.now() - t0).toFixed(0)} ms, ${calls.length} calls recorded`)

// --- results -----------------------------------------------------------------------
type Out = { form: Form; text: string }
function canon(form: Form, n: number, ids: Uint32Array, width: number): string {
  if (form === Form.Ask) return String(n === 1)
  const rows: string[] = []
  // Blank nodes compare as one: they are fresh per run.
  const id = (x: number) => (x && e.kind(x) === Kind.BlankNode ? 'b' : String(x))
  for (let i = 0; i < ids.length; i += width) rows.push(Array.from(ids.subarray(i, i + width), id).join(','))
  rows.sort()
  if (form !== Form.Select) return [...new Set(rows)].join(' ')
  return `${n}: ` + rows.join(' ')
}
function result(q: Query, n: number): Out {
  const width = q.form === Form.Select ? q.vars.length : 3
  const ids = q.form === Form.Ask ? new Uint32Array() : e.resultIds(n * width)
  return { form: q.form, text: canon(q.form, n, ids, Math.max(width, 1)) }
}

const median = (xs: number[]) => {
  const s = [...xs].sort((a, b) => a - b)
  return s.length ? s[Math.floor(s.length / 2)]! : NaN
}
const pct = (xs: number[], p: number) => {
  const s = [...xs].sort((a, b) => a - b)
  return s.length ? s[Math.min(s.length - 1, Math.floor(s.length * p))]! : NaN
}

/** Time `f` per call: the median of single runs, at least 5 and up to ~3 ms or 200 runs. */
function time(f: () => void): number {
  f()
  const ts: number[] = []
  const start = performance.now()
  while (ts.length < 5 || (ts.length < 200 && performance.now() - start < 3)) {
    const a = performance.now()
    f()
    ts.push(performance.now() - a)
  }
  return median(ts)
}

const prefixed = (q: string) => SPARQL_PREFIXES + '\nPREFIX ui: <https://semblance.dev/demo#>\nPREFIX ex: <https://example.org/todo#>\nPREFIX perf: <https://semblance.dev/ns/perf#>\n' + q

// --- 1. coverage ---------------------------------------------------------------------
const corpus = semblanceQueries()
const declined = new Map<string, number>()
let compilable = 0
let unprepared = 0
const compilableTexts: string[] = []
for (const raw of corpus) {
  const text = prefixed(raw)
  let q: Query
  try {
    q = e.prepare(text)
  } catch {
    unprepared++
    continue
  }
  for (const m of new Set([...raw.matchAll(/\$(\w+)/g)].map((m) => m[1]!))) q.bind(m, e.iri('https://example.org/focus'))
  const b = e.compiledBytes(q.handle)
  if (typeof b === 'string') declined.set(b, (declined.get(b) ?? 0) + 1)
  else {
    compilable++
    if (!/\$(this|event)\b/.test(raw)) compilableTexts.push(text)
  }
  q.release()
}
console.error(`coverage: ${compilable}/${corpus.length - unprepared} compile`, declined)

// --- profiling: what leaves a compiled module ------------------------------------------
// The callbacks a compiled run makes, each timed, and named by what it was called
// for. Naming reads the WAT engine's own nodes (k is a node pointer there: its kind
// at +0, an E_CALL's function at +4), so the breakdown is for wat; rust shows counts.
const defines = new Map<number, string>()
if (kind === 'wat') {
  const src = readFileSync(new URL('../wat/sparql.wat', import.meta.url), 'utf8')
  for (const m of src.matchAll(/@define ((?:F|FN|P|E|X)_\w+) (\d+)/g)) {
    const name = m[1]!, n = Number(m[2])
    defines.set((name.startsWith('F') ? 1000 : name.startsWith('E') ? 2000 : name.startsWith('X') ? 4000 : 3000) + n, name)
  }
}
const engineX = (store.engine as unknown as { x: Record<string, unknown> & { memory: WebAssembly.Memory } }).x
function nodeName(k: number, expr: boolean): string {
  if (kind !== 'wat') return ''
  const m = new Int32Array(engineX.memory.buffer)
  // cq_eval's k | 1 (inside a LATERAL compiled in line): a record whose +4 is
  // the node; cq_value's and cq_test's k | 1 is a flag
  const at = k & 1 && !expr ? m[((k & ~1) >> 2) + 1]! >> 2 : (k & ~1) >> 2
  const kindNo = m[at]!
  if (!expr) return defines.get(3000 + kindNo) ?? defines.get(4000 + kindNo) ?? `node ${kindNo}`
  if (kindNo === 32) {
    const f = m[at + 1]!
    return defines.get(1000 + f) ?? `F ${f}`
  }
  return defines.get(2000 + kindNo) ?? `E ${kindNo}`
}
function profileRun(module: WebAssembly.Module, graphs: number[]): Record<string, [number, number]> {
  const out: Record<string, [number, number]> = {}
  const wrapped: Record<string, unknown> = { ...engineX }
  for (const name of ['cq_eval', 'cq_path', 'cq_test', 'cq_value', 'cq_scan', 'cq_triple', 'cq_solution', 'cq_group', 'cq_keep', 'cq_iri']) {
    const f = engineX[name] as (...a: number[]) => number
    wrapped[name] = (...a: number[]) => {
      const what = name === 'cq_eval' || name === 'cq_path' ? nodeName(a[0]!, false) : name === 'cq_test' || name === 'cq_value' ? nodeName(a[0]!, true) : ''
      const t = performance.now()
      const r = f(...a)
      const key = what ? `${name}:${what}` : name
      const e = (out[key] ??= [0, 0])
      e[0]++
      e[1] += performance.now() - t
      return r
    }
  }
  const inst = new WebAssembly.Instance(module, { engine: wrapped as WebAssembly.ModuleImports }).exports as unknown as { run(g: number): number }
  const at = (engineX.input as (n: number) => number)(graphs.length * 4)
  new Uint32Array(engineX.memory.buffer, at, graphs.length).set(graphs)
  inst.run(graphs.length)
  return out
}

// --- 2. recorded calls ---------------------------------------------------------------
const seen = new Set<string>()
const distinct: Call[] = []
for (const c of calls) {
  const k = `${c.method}\u0000${c.text}\u0000${c.variable}\u0000${c.values.join(',')}\u0000${c.graphs.join(',')}`
  if (seen.has(k)) continue
  seen.add(k)
  distinct.push(c)
}
interface Measured {
  text: string
  method: string
  foci: number
  compiled: boolean
  reason?: string
  same?: boolean
  interpMs: number
  compiledMs?: number
  emitMs?: number
  wasmCompileMs?: number
  instantiateMs?: number
  bytes?: number
  /** --profile: per callback (and what it was called for): calls and ms, in one compiled run. */
  callbacks?: Record<string, [number, number]>
}
const measured: Measured[] = []
const mismatches: string[] = []
for (const c of distinct.slice(0, maxCalls)) {
  const q = e.prepare(c.text)
  try {
    if (c.method === 'constructFor') q.bindValues(c.variable!, c.values)
    const interp = () => e.runQuery(q.handle, c.graphs)
    const n = interp()
    const expected = result(q, n)
    const interpMs = time(interp)
    const m: Measured = { text: c.text, method: c.method, foci: c.values.length, compiled: false, interpMs }
    measured.push(m)
    let a = performance.now()
    const bytes = e.compiledBytes(q.handle)
    m.emitMs = performance.now() - a
    if (typeof bytes === 'string') {
      m.reason = bytes
      continue
    }
    a = performance.now()
    const module = await WebAssembly.compile(bytes)
    m.wasmCompileMs = performance.now() - a
    a = performance.now()
    const inst = e.instantiateCompiled(module)
    m.instantiateMs = performance.now() - a
    m.bytes = bytes.length
    const run = () => e.runCompiled(inst, c.graphs)
    const k = run()
    if (k < 0) throw new Error('compiled run failed: ' + k)
    const got = result(q, k)
    m.compiled = true
    m.same = got.text === expected.text
    if (!m.same) mismatches.push(`${c.text}\n  interpreted ${expected.text.slice(0, 300)}\n  compiled    ${got.text.slice(0, 300)}`)
    m.compiledMs = time(run)
    if (profile) m.callbacks = profileRun(module, c.graphs)
  } finally {
    q.release()
  }
}

// --- 3. unbound runs of every compilable corpus query --------------------------------
let unboundSame = 0
let unboundRun = 0
const memoryMb = () => (store.engine as unknown as { x: { memory: WebAssembly.Memory } }).x.memory.buffer.byteLength >> 20
for (const text of compilableTexts) {
  if (memoryMb() > 512) break
  const q = e.prepare(text)
  try {
    const a = performance.now()
    const n = e.runQuery(q.handle, [])
    if (performance.now() - a > 200) continue
    const expected = result(q, n)
    const bytes = e.compiledBytes(q.handle)
    if (typeof bytes === 'string') continue
    const k = e.runCompiled(e.instantiateCompiled(new WebAssembly.Module(bytes)), [])
    unboundRun++
    const got = result(q, k)
    if (got.text === expected.text) unboundSame++
    else mismatches.push(`(unbound) ${text.split('\n').filter((l) => !/^PREFIX/i.test(l)).join(' ').slice(0, 400)}\n  interpreted ${expected.text.slice(0, 300)}\n  compiled    ${got.text.slice(0, 300)}`)
  } finally {
    q.release()
  }
}

// --- report --------------------------------------------------------------------------
if (profile) {
  // Call sites (query texts) by total compiled time, with what leaves the module.
  const sites = new Map<string, { calls: number; interp: number; compiled: number; cb: Record<string, [number, number]> }>()
  for (const m of measured) {
    if (!m.compiled) continue
    const site = sites.get(m.text) ?? { calls: 0, interp: 0, compiled: 0, cb: {} }
    site.calls++
    site.interp += m.interpMs
    site.compiled += m.compiledMs!
    for (const [k, [n, ms]] of Object.entries(m.callbacks ?? {})) {
      const e = (site.cb[k] ??= [0, 0])
      e[0] += n
      e[1] += ms
    }
    sites.set(m.text, site)
  }
  const ranked = [...sites.entries()].sort((a, b) => b[1].compiled - a[1].compiled)
  const total = ranked.reduce((a, [, s]) => a + s.compiled, 0)
  console.error(`\ntop call sites by compiled time (${kind}; ${total.toFixed(1)} ms over ${ranked.length} sites)`)
  ranked.slice(0, 20).forEach(([text, s], i) => {
    const body = text.split('\n').filter((l) => !/^PREFIX/i.test(l)).join(' ').replace(/\s+/g, ' ')
    const head = /WHERE \{ *(.{0,90})/.exec(body)?.[1] ?? body.slice(0, 90)
    const cb = Object.entries(s.cb).sort((a, b) => b[1][1] - a[1][1]).slice(0, 6).map(([k, [n, ms]]) => `${k} ${n}x ${ms.toFixed(2)}ms`).join('; ')
    console.error(`${i + 1}. ${s.compiled.toFixed(2)} ms compiled (${s.interp.toFixed(2)} interp), ${s.calls} calls | ${head}\n     ${cb}`)
  })
}
const comp = measured.filter((m) => m.compiled)
const ratio = comp.map((m) => m.interpMs / m.compiledMs!)
const overhead = comp.map((m) => m.emitMs! + m.wasmCompileMs! + m.instantiateMs!)
const breakEven = comp.map((m) => {
  const saved = m.interpMs - m.compiledMs!
  return saved > 0 ? (m.emitMs! + m.wasmCompileMs! + m.instantiateMs!) / saved : Infinity
})
// Per query text: the calls' total time interpreted vs compiled (what a render pays).
const byText = new Map<string, { interp: number; compiled: number; overhead: number; calls: number }>()
for (const m of comp) {
  const t = byText.get(m.text) ?? { interp: 0, compiled: 0, overhead: 0, calls: 0 }
  t.interp += m.interpMs
  t.compiled += m.compiledMs!
  t.overhead = Math.max(t.overhead, m.emitMs! + m.wasmCompileMs! + m.instantiateMs!)
  t.calls++
  byText.set(m.text, t)
}
const declinedCalls = new Map<string, number>()
for (const m of measured) if (!m.compiled && m.reason) declinedCalls.set(m.reason, (declinedCalls.get(m.reason) ?? 0) + 1)
const f3 = (x: number) => (x >= 100 ? x.toFixed(0) : x >= 10 ? x.toFixed(1) : x.toFixed(3))
const summary = {
  engine: kind,
  corpus: { queries: corpus.length, prepared: corpus.length - unprepared, compiled: compilable, declined: Object.fromEntries(declined) },
  calls: { recorded: calls.length, distinct: distinct.length, measured: measured.length, compiled: comp.length, same: comp.filter((m) => m.same).length, declined: Object.fromEntries(declinedCalls) },
  unbound: { run: unboundRun, same: unboundSame },
  perCall: {
    interpMs: { median: median(comp.map((m) => m.interpMs)), p90: pct(comp.map((m) => m.interpMs), 0.9) },
    compiledMs: { median: median(comp.map((m) => m.compiledMs!)), p90: pct(comp.map((m) => m.compiledMs!), 0.9) },
    speedup: { median: median(ratio), p10: pct(ratio, 0.1), p90: pct(ratio, 0.9) },
    emitMs: { median: median(comp.map((m) => m.emitMs!)), p90: pct(comp.map((m) => m.emitMs!), 0.9) },
    wasmCompileMs: { median: median(comp.map((m) => m.wasmCompileMs!)), p90: pct(comp.map((m) => m.wasmCompileMs!), 0.9) },
    instantiateMs: { median: median(comp.map((m) => m.instantiateMs!)), p90: pct(comp.map((m) => m.instantiateMs!), 0.9) },
    overheadMs: { median: median(overhead), p90: pct(overhead, 0.9) },
    bytes: { median: median(comp.map((m) => m.bytes!)), p90: pct(comp.map((m) => m.bytes!), 0.9) },
    breakEvenRuns: { median: median(breakEven), p90: pct(breakEven, 0.9), never: breakEven.filter((x) => x === Infinity).length },
  },
  totals: {
    interpMsAllCalls: measured.reduce((a, m) => a + m.interpMs, 0),
    interpMsCompiledCalls: comp.reduce((a, m) => a + m.interpMs, 0),
    compiledMsCompiledCalls: comp.reduce((a, m) => a + m.compiledMs!, 0),
    overheadMsDistinctQueries: [...byText.values()].reduce((a, t) => a + t.overhead, 0),
    distinctCompiledQueries: byText.size,
  },
  mismatches: mismatches.length,
}
console.log(JSON.stringify(summary, null, 2))
for (const m of mismatches.slice(0, 10)) console.error('MISMATCH ' + m)
const out = opt('--out')
if (out) writeFileSync(out, JSON.stringify({ summary, measured: measured.map((m) => ({ ...m, text: m.text.split('\n').filter((l) => !/^PREFIX/i.test(l)).join('\n') })), mismatches }, null, 1))
void f3
process.exit(0)
