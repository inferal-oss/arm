// W3C conformance runner: `npm run conformance -- [--target oxigraph] [--suite sparql12 ...] [--only <substring>] [-v]`
// Writes results/<target>.json (failures by test IRI) unless filtered with --only.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { loadManifest, localPath, rdfFile, readText, ROOT, type Test } from './manifest.ts'
import { fromResultGraph, parseSrj, parseSrx } from './results.ts'
import type { Dataset, QueryResult, Target } from './target.ts'
import { oxigraph, oxParse } from './targets/oxigraph.ts'
import { isomorphic, key, type Row, rowKey, type T } from './terms.ts'

export const SUITES: Record<string, string[]> = {
  sparql12: ['sparql/sparql12/manifest.ttl'],
  sparql11: ['sparql/sparql11/manifest-sparql11-query.ttl', 'sparql/sparql11/manifest-sparql11-update.ttl'],
  sparql10: ['sparql/sparql10/manifest-evaluation.ttl', 'sparql/sparql10/manifest-syntax.ttl'],
  rdf12: ['rdf/rdf12/rdf-turtle/manifest.ttl', 'rdf/rdf12/rdf-n-triples/manifest.ttl', 'rdf/rdf12/rdf-n-quads/manifest.ttl', 'rdf/rdf12/rdf-trig/manifest.ttl'],
}

const TARGETS: Record<string, () => Promise<Target>> = {
  oxigraph: async () => oxigraph,
  rust: async () => (await import('./targets/wasm.ts')).wasmTarget('rust'),
  wat: async () => (await import('./targets/wasm.ts')).wasmTarget('wat'),
}

/**
 * `spelling`: the result is right up to how numbers are written ("2" for "2.0"^^xsd:decimal,
 * "2.5" for "2.5E0"^^xsd:double): the engines write numbers as Oxigraph does, and some
 * tests expect XSD 1.0 canonical forms.
 */
type Outcome = { status: 'pass' } | { status: 'fail'; reason: string } | { status: 'skip'; reason: string } | { status: 'spelling' }

const SKIPPED_APPROVALS = new Set(['Withdrawn', 'Rejected', 'NotApproved'])
const RDF_TEST = /^Test(Turtle|NTriples|NQuads|Trig|TriG)(PositiveSyntax|NegativeSyntax|Eval|NegativeEval)$/

const short = (e: unknown) => String((e as Error)?.message ?? e).split('\n')[0]!.slice(0, 160)

function dataset(t: Test, query?: string): Dataset {
  const ds: Dataset = {
    default: t.data.map((d) => rdfFile(d)),
    named: t.graphData.map((g) => ({ name: g.name, file: rdfFile(g.file) })),
  }
  // FROM / FROM NAMED name files of the suite: provide them as named graphs.
  if (query) {
    for (const m of query.matchAll(/FROM\s+(?:NAMED\s+)?<([^>]*)>/gi)) {
      const name = new URL(m[1]!, t.query).href
      if (name.startsWith(ROOT) && existsSync(localPath(name)) && !ds.named.some((n) => n.name === name)) ds.named.push({ name, file: rdfFile(name) })
    }
  }
  return ds
}

function expected(file: string): QueryResult {
  if (file.endsWith('.srj')) return parseSrj(readText(file))
  if (file.endsWith('.srx')) return parseSrx(readText(file))
  const quads = oxParse(rdfFile(file))
  return fromResultGraph(quads) ?? { kind: 'graph', quads }
}

const triples = (rows: Row[]) => rows.map((r) => r.slice(0, 3))

const XSD = 'http://www.w3.org/2001/XMLSchema#'
const DECIMALS = new Set(['integer', 'decimal', 'int', 'long', 'short', 'byte', 'nonNegativeInteger', 'positiveInteger', 'nonPositiveInteger', 'negativeInteger', 'unsignedInt', 'unsignedLong', 'unsignedShort', 'unsignedByte'])

/** A number's value as one string, whatever its lexical form; other terms as they are. */
function byValue(t: T | undefined): T | undefined {
  if (t?.t !== 'lit' || !t.dt.startsWith(XSD)) return t
  const local = t.dt.slice(XSD.length)
  let v = t.v.trim()
  if (local === 'double' || local === 'float') {
    const n = v === 'INF' ? Infinity : v === '-INF' ? -Infinity : Number(v)
    v = Object.is(n, -0) ? '-0' : String(n)
  } else if (DECIMALS.has(local) && /^[+-]?(\d+(\.\d*)?|\.\d+)$/.test(v)) {
    const neg = v.startsWith('-')
    let [i, f = ''] = v.replace(/^[+-]/, '').split('.')
    i = i!.replace(/^0+(?=\d)/, '') || '0'
    f = f.replace(/0+$/, '')
    v = (neg && (i !== '0' || f) ? '-' : '') + i + (f ? '.' + f : '')
  } else return t
  return { ...t, v }
}
const valueRows = (rows: Row[]) => rows.map((r) => r.map(byValue))

/** `--show`: print actual and expected results of failing evaluation tests. */
let show = false
const render = (r: QueryResult) =>
  r.kind === 'boolean'
    ? String(r.value)
    : r.kind === 'graph'
      ? r.quads.map((q) => '  ' + rowKey(q.slice(0, 3))).join('\n')
      : r.rows.map((m) => '  ' + [...m].map(([k, v]) => `?${k}=${key(v)}`).join(' ')).join('\n')

function compare(actual: QueryResult, want: QueryResult, ordered: boolean): Outcome {
  const o = compareAs(actual, want, ordered, (rows) => rows)
  if (o.status !== 'fail' || o.reason !== 'wrong result') return o
  return compareAs(actual, want, ordered, valueRows).status === 'pass' ? { status: 'spelling' } : o
}

function compareAs(actual: QueryResult, want: QueryResult, ordered: boolean, view: (rows: Row[]) => Row[]): Outcome {
  if (actual.kind !== want.kind) return { status: 'fail', reason: `expected ${want.kind} result, got ${actual.kind}` }
  let same: boolean | undefined
  if (actual.kind === 'boolean') same = actual.value === (want as typeof actual).value
  else if (actual.kind === 'graph') same = isomorphic(view(triples(actual.quads)), view(triples((want as typeof actual).quads)), false)
  else {
    const w = want as typeof actual
    const vars = [...new Set([...actual.vars, ...w.vars, ...actual.rows.flatMap((r) => [...r.keys()]), ...w.rows.flatMap((r) => [...r.keys()])])].sort()
    const rows = (rs: Map<string, unknown>[]) => view(rs.map((r) => vars.map((v) => r.get(v))) as Row[])
    same = isomorphic(rows(actual.rows), rows(w.rows), ordered)
    if (same === false && ordered && isomorphic(rows(actual.rows), rows(w.rows), false)) return { status: 'fail', reason: 'wrong order' }
  }
  if (same === undefined) return { status: 'fail', reason: 'comparison too hard (blank nodes)' }
  return same ? { status: 'pass' } : { status: 'fail', reason: 'wrong result' }
}

/** An engine that has not reached a feature yet says so; that is a skip, not a pass or a fail. */
const NOT_IMPLEMENTED = 'not implemented'

function expectError(run: () => unknown): Outcome {
  try {
    run()
  } catch (e) {
    if (short(e) === NOT_IMPLEMENTED) return { status: 'skip', reason: NOT_IMPLEMENTED }
    return { status: 'pass' }
  }
  return { status: 'fail', reason: 'accepted invalid input' }
}

export function runTest(target: Target, t: Test): Outcome {
  if (t.approval && SKIPPED_APPROVALS.has(t.approval)) return { status: 'skip', reason: `approval: ${t.approval}` }
  try {
    switch (t.type) {
      case 'PositiveSyntaxTest':
      case 'PositiveSyntaxTest11':
        target.parseQuery(readText(t.action!), t.action!)
        return { status: 'pass' }
      case 'NegativeSyntaxTest':
      case 'NegativeSyntaxTest11':
        return expectError(() => target.parseQuery(readText(t.action!), t.action!))
      case 'PositiveUpdateSyntaxTest':
      case 'PositiveUpdateSyntaxTest11':
        target.parseUpdate(readText(t.action!), t.action!)
        return { status: 'pass' }
      case 'NegativeUpdateSyntaxTest':
      case 'NegativeUpdateSyntaxTest11':
        return expectError(() => target.parseUpdate(readText(t.action!), t.action!))
      case 'QueryEvaluationTest': {
        const query = readText(t.query!)
        if (typeof t.result !== 'string') return { status: 'skip', reason: 'no expected result' }
        if (/\.(csv|tsv)$/.test(t.result)) return { status: 'skip', reason: 'CSV/TSV results' }
        const actual = target.query(dataset(t, query), query, t.query!)
        const want = expected(t.result)
        const o = compare(actual, want, /ORDER\s+BY/i.test(query))
        if (show && o.status === 'fail') console.log(`${t.id}\n${query}\nACTUAL\n${render(actual)}\nEXPECTED\n${render(want)}\n`)
        return o
      }
      case 'UpdateEvaluationTest': {
        const after = target.update(dataset(t), readText(t.request!), t.request!)
        const r = t.result as { data: string[]; graphData: { name: string; file: string }[] }
        const want: Row[] = [
          ...r.data.flatMap((d) => oxParse(rdfFile(d))),
          ...r.graphData.flatMap((g) => oxParse(rdfFile(g.file)).map((q) => [q[0], q[1], q[2], { t: 'iri', v: g.name }] as Row)),
        ]
        const same = isomorphic(after, want, false)
        return same ? { status: 'pass' } : { status: 'fail', reason: same === undefined ? 'comparison too hard (blank nodes)' : 'wrong dataset' }
      }
    }
    const rdf = RDF_TEST.exec(t.type)
    if (rdf) {
      const file = rdfFile(t.action!)
      switch (rdf[2]) {
        case 'PositiveSyntax':
          target.parse(file)
          return { status: 'pass' }
        case 'NegativeSyntax':
        case 'NegativeEval':
          return expectError(() => target.parse(file))
        case 'Eval': {
          const same = isomorphic(target.parse(file), oxParse(rdfFile(t.result as string)), false)
          return same ? { status: 'pass' } : { status: 'fail', reason: 'wrong quads' }
        }
      }
    }
    return { status: 'skip', reason: `test type ${t.type}` }
  } catch (e) {
    if (short(e) === NOT_IMPLEMENTED) return { status: 'skip', reason: NOT_IMPLEMENTED }
    return { status: 'fail', reason: 'error: ' + short(e) }
  }
}

async function main() {
  const args = process.argv.slice(2)
  const opt = (name: string) => {
    const out: string[] = []
    for (let i = 0; i < args.length; i++) if (args[i] === name && args[i + 1]) out.push(args[++i]!)
    return out
  }
  const targetName = opt('--target')[0] ?? 'oxigraph'
  const suites = opt('--suite').length ? opt('--suite') : Object.keys(SUITES)
  const only = opt('--only')[0]
  const verbose = args.includes('-v')
  show = args.includes('--show')
  const target = await TARGETS[targetName]!()

  const summary: Record<string, { pass: number; fail: number; skip: number; spelling: number }> = {}
  const failing: Record<string, string> = {}
  const spelling: string[] = []
  for (const suite of suites) {
    const s = (summary[suite] = { pass: 0, fail: 0, skip: 0, spelling: 0 })
    const seen = new Set<string>()
    for (const t of SUITES[suite]!.flatMap((m) => loadManifest(m, seen))) {
      if (only && !t.id.includes(only)) continue
      const o = runTest(target, t)
      s[o.status]++
      if (o.status === 'spelling') spelling.push(t.id.startsWith(ROOT) ? t.id.slice(ROOT.length) : t.id)
      if (o.status === 'fail') {
        failing[t.id.startsWith(ROOT) ? t.id.slice(ROOT.length) : t.id] = o.reason
        if (verbose) console.log(`FAIL ${t.id}\n     ${t.name}: ${o.reason}`)
      }
    }
  }

  console.log(`\n${target.name}\n\n| suite | pass | spelling | fail | skip |\n|---|---:|---:|---:|---:|`)
  for (const [suite, s] of Object.entries(summary)) console.log(`| ${suite} | ${s.pass} | ${s.spelling} | ${s.fail} | ${s.skip} |`)
  if (only) return
  const dir = new URL('./results/', import.meta.url)
  mkdirSync(dir, { recursive: true })
  const commit = readFileSync(new URL('./fetch.sh', import.meta.url), 'utf8').match(/COMMIT=(\w+)/)![1]
  const sorted = Object.fromEntries(Object.entries(failing).sort(([a], [b]) => a.localeCompare(b)))
  writeFileSync(new URL(`${target.name}.json`, dir), JSON.stringify({ target: target.name, rdfTests: commit, summary, failing: sorted, spelling: spelling.sort() }, null, 2) + '\n')
}

if (import.meta.url === `file://${process.argv[1]}`) await main()
