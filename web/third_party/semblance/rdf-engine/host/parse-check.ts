// Prepare every SPARQL query Semblance runs (rule bodies and query literals in
// the ontology and demo, query strings in src/) with an engine build, and report
// the ones that fail: `npx tsx rdf-engine/host/parse-check.ts [rust|wat] [-v]`
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { Parser } from 'n3'
import { SPARQL_PREFIXES } from '../../src/ns.ts'
import { Engine } from './engine.ts'

const root = fileURLToPath(new URL('../../', import.meta.url))
const walk = (d: string): string[] => readdirSync(d).flatMap((f) => (statSync(d + '/' + f).isDirectory() ? walk(d + '/' + f) : [d + '/' + f]))
const looksLikeQuery = (s: string) => s.includes('{') && /^\s*(PREFIX|BASE|SELECT|CONSTRUCT|ASK|DESCRIBE)\b/i.test(s.replace(/#[^\n]*\n/g, ''))

export function semblanceQueries(): string[] {
  const out: string[] = []
  for (const f of [...walk(root + 'ontology'), ...walk(root + 'demo')].filter((f) => f.endsWith('.ttl'))) {
    // A file's own prefixes come along with its queries (example apps declare theirs).
    const prefixes: string[] = []
    const quads = new Parser().parse(readFileSync(f, 'utf8'), null, (p, iri) => void prefixes.push(`PREFIX ${p}: <${iri.value}>`))
    for (const q of quads) if (q.object.termType === 'Literal' && looksLikeQuery(q.object.value)) out.push(prefixes.join('\n') + '\n' + q.object.value)
  }
  for (const f of walk(root + 'src').filter((f) => f.endsWith('.ts'))) {
    // Template literals without interpolation that are whole queries.
    for (const m of readFileSync(f, 'utf8').matchAll(/`([^`$]*)`/g)) if (looksLikeQuery(m[1]!)) out.push(m[1]!)
  }
  return out
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const name = process.argv[2] ?? 'rust'
  const verbose = process.argv.includes('-v')
  const module = new WebAssembly.Module(readFileSync(new URL(`../build/${name}.wasm`, import.meta.url)))
  const e = new Engine(module)
  const queries = semblanceQueries()
  let failed = 0
  for (const q of queries) {
    // Rules use $this; the shell's own queries carry the demo's prefixes.
    // Maintained views' parameters (iv:parameter) are substituted before they run: a number where SPARQL needs one.
    const text = SPARQL_PREFIXES + '\nPREFIX ui: <https://semblance.dev/demo#>\nPREFIX ex: <https://example.org/todo#>\nPREFIX perf: <https://semblance.dev/ns/perf#>\n' + q.replaceAll('$this', '<https://example.org/this>').replace(/\b(LIMIT|OFFSET)\s+\$\w+/g, '$1 1')
    try {
      e.prepare(text).release()
    } catch (err) {
      failed++
      const message = (err as Error).message
      const line = Number(/line (\d+)/.exec(message)?.[1] ?? 0)
      console.log(`${message}\n    ${text.split('\n')[line - 1]?.trim()}`)
      if (verbose) console.log(text + '\n')
    }
  }
  console.log(`${name}: ${queries.length - failed}/${queries.length} Semblance queries prepare`)
  process.exitCode = failed ? 1 : 0
}
