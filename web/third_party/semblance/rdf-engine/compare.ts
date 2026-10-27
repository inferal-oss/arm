// Size and speed of the engine builds next to Oxigraph and N3.js, on the same
// synthetic data: `npm run engine:compare [-- rust wat ...]` (build first). Writes results/compare.md.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { brotliCompressSync, gzipSync } from 'node:zlib'
import * as N3 from 'n3'
import * as ox from 'oxigraph'
import { DEFAULT_GRAPH, Engine, Format } from './host/engine.ts'

const here = fileURLToPath(new URL('.', import.meta.url))
const QUADS = 100_000
const LOOKUPS = 20_000

// Deterministic synthetic graph: 10k subjects, 20 predicates, objects half IRIs, half literals.
let seed = 42
const rand = (n: number) => ((seed = (Math.imul(seed, 1103515245) + 12345) & 0x7fffffff), (seed >>> 8) % n)
type Spec = [string, string, { iri: string } | { lit: string }]
const specs: Spec[] = []
const seen = new Set<string>()
while (specs.length < QUADS) {
  const s = `http://example.org/s${rand(10_000)}`
  const p = `http://example.org/p${rand(20)}`
  const o = rand(2) ? { iri: `http://example.org/o${rand(50_000)}` } : { lit: `value ${rand(50_000)}` }
  const k = s + p + JSON.stringify(o)
  if (seen.has(k)) continue
  seen.add(k)
  specs.push([s, p, o])
}
const ntriples = specs.map(([s, p, o]) => `<${s}> <${p}> ${'iri' in o ? `<${o.iri}>` : JSON.stringify(o.lit)} .`).join('\n') + '\n'
const lookups = Array.from({ length: LOOKUPS }, () => specs[rand(specs.length)]!)

/** µs per op: best of three runs of `f` over `n` ops. */
function time(n: number, f: () => void): number {
  let best = Infinity
  for (let r = 0; r < 3; r++) {
    const t = performance.now()
    f()
    best = Math.min(best, performance.now() - t)
  }
  return (best * 1000) / n
}

interface Row {
  load: number
  add: number
  match: number
  matchRead: number
  churn: number
  memory?: number
}

function wasmEngine(module: WebAssembly.Module): Row {
  const row = {} as Row
  row.load = time(QUADS, () => new Engine(module).load(Format.NTriples, ntriples))
  const e = new Engine(module)
  const ids = specs.map(([s, p, o]) => [e.iri(s), e.iri(p), 'iri' in o ? e.iri(o.iri) : e.literal(o.lit)] as const)
  row.add = time(QUADS, () => {
    for (const [s, p, o] of ids) e.add(s, p, o, DEFAULT_GRAPH)
  })
  const keys = lookups.map(([s, p]) => [e.iri(s), e.iri(p)] as const)
  row.match = time(LOOKUPS, () => {
    for (const [s, p] of keys) e.match(s, p, 0, 0)
  })
  // Host-side reads: object values as strings, with the id -> string cache a store adapter keeps.
  row.matchRead = time(LOOKUPS, () => {
    const cache = new Map<number, string>()
    for (const [s, p] of keys) {
      const r = e.match(s, p, 0, 0)
      for (let i = 2; i < r.length; i += 4) if (!cache.has(r[i]!)) cache.set(r[i]!, e.value(r[i]!))
    }
  })
  row.churn = time(LOOKUPS * 2, () => {
    for (let i = 0; i < LOOKUPS; i++) {
      const [s, p, o] = ids[i]!
      e.remove(s, p, o, DEFAULT_GRAPH)
      e.add(s, p, o, DEFAULT_GRAPH)
    }
  })
  row.memory = (e as unknown as { x: { memory: WebAssembly.Memory } }).x.memory.buffer.byteLength
  return row
}

function oxigraph(): Row {
  const row = {} as Row
  row.load = time(QUADS, () => new ox.Store().load(ntriples, { format: 'application/n-triples' }))
  const store = new ox.Store()
  const quads = specs.map(([s, p, o]) => ox.quad(ox.namedNode(s), ox.namedNode(p), 'iri' in o ? ox.namedNode(o.iri) : ox.literal(o.lit), ox.defaultGraph()))
  row.add = time(QUADS, () => {
    for (const q of quads) store.add(q)
  })
  const keys = lookups.map(([s, p]) => [ox.namedNode(s), ox.namedNode(p)] as const)
  row.match = time(LOOKUPS, () => {
    for (const [s, p] of keys) store.match(s, p, null, null)
  })
  row.matchRead = time(LOOKUPS, () => {
    for (const [s, p] of keys) for (const q of store.match(s, p, null, null)) void q.object.value
  })
  row.churn = time(LOOKUPS * 2, () => {
    for (let i = 0; i < LOOKUPS; i++) {
      store.delete(quads[i]!)
      store.add(quads[i]!)
    }
  })
  return row
}

function n3(): Row {
  const { namedNode, literal, quad, defaultGraph } = N3.DataFactory
  const row = {} as Row
  row.load = time(QUADS, () => new N3.Store().addQuads(new N3.Parser({ format: 'N-Triples' }).parse(ntriples)))
  const store = new N3.Store()
  const quads = specs.map(([s, p, o]) => quad(namedNode(s), namedNode(p), 'iri' in o ? namedNode(o.iri) : literal(o.lit), defaultGraph()))
  row.add = time(QUADS, () => {
    for (const q of quads) store.addQuad(q)
  })
  const keys = lookups.map(([s, p]) => [namedNode(s), namedNode(p)] as const)
  row.match = time(LOOKUPS, () => {
    for (const [s, p] of keys) store.getQuads(s, p, null, null)
  })
  row.matchRead = time(LOOKUPS, () => {
    for (const [s, p] of keys) for (const q of store.getQuads(s, p, null, null)) void q.object.value
  })
  row.churn = time(LOOKUPS * 2, () => {
    for (let i = 0; i < LOOKUPS; i++) {
      store.removeQuad(quads[i]!)
      store.addQuad(quads[i]!)
    }
  })
  return row
}

const results: [string, Row, string][] = []
const wanted = process.argv.slice(2)
for (const name of wanted.length ? wanted : ['rust', 'wat']) {
  const path = here + `build/${name}.wasm`
  if (!existsSync(path)) continue
  const bytes = readFileSync(path)
  const size = `${(bytes.length / 1024).toFixed(1)} / ${(gzipSync(bytes, { level: 9 }).length / 1024).toFixed(1)} / ${(brotliCompressSync(bytes).length / 1024).toFixed(1)}`
  results.push([name, wasmEngine(new WebAssembly.Module(bytes)), size])
}
const oxWasm = readFileSync(fileURLToPath(import.meta.resolve('oxigraph/node_bg.wasm')))
results.push(['oxigraph', oxigraph(), `${(oxWasm.length / 1024).toFixed(1)} / ${(gzipSync(oxWasm, { level: 9 }).length / 1024).toFixed(1)} / ${(brotliCompressSync(oxWasm).length / 1024).toFixed(1)}`])
results.push(['n3', n3(), 'JS'])

const f = (n: number) => (n < 10 ? n.toFixed(2) : n.toFixed(1))
const lines = [
  `## ${new Date().toISOString().slice(0, 10)} · Node ${process.version} (${process.platform}/${process.arch})`,
  '',
  `${QUADS.toLocaleString('en')} quads; ${LOOKUPS.toLocaleString('en')} lookups of (s, p, ?) returning about ${(QUADS / 10_000 / 20).toFixed(1)} quads each. µs per op, best of 3.`,
  '',
  '| engine | wasm KiB (raw / gzip / brotli) | load N-Triples | add | match(s, p, ?) | match + read objects | remove + add | memory |',
  '|---|---:|---:|---:|---:|---:|---:|---:|',
  ...results.map(([name, r, size]) => `| ${name} | ${size} | ${f(r.load)} | ${f(r.add)} | ${f(r.match)} | ${f(r.matchRead)} | ${f(r.churn)} | ${r.memory ? (r.memory / 2 ** 20).toFixed(1) + ' MiB' : ''} |`),
  '',
]
console.log(lines.join('\n'))
mkdirSync(here + 'results', { recursive: true })
writeFileSync(here + 'results/compare.md', '# Engine comparison\n\n' + lines.join('\n'))
