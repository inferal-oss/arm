// Reads W3C test manifests (mf:, qt:, ut:, rdft:) into plain test records.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type { RdfFile, RdfFormat } from './target.ts'
import { oxParse } from './targets/oxigraph.ts'
import { key, type Row, type T } from './terms.ts'

/** Where rdf-tests publishes the suites; files are resolved against it. */
export const ROOT = 'https://w3c.github.io/rdf-tests/'
export const TESTS_DIR = fileURLToPath(new URL('./rdf-tests/', import.meta.url))

const MF = 'http://www.w3.org/2001/sw/DataAccess/tests/test-manifest#'
const QT = 'http://www.w3.org/2001/sw/DataAccess/tests/test-query#'
const UT = 'http://www.w3.org/2009/sparql/tests/test-update#'
const DAWGT = 'http://www.w3.org/2001/sw/DataAccess/tests/test-dawg#'
const RDF = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#'
const RDFS = 'http://www.w3.org/2000/01/rdf-schema#'

export interface GraphFile {
  name: string
  file: string
}

export interface Test {
  id: string
  /** Local name of the test's type, e.g. `QueryEvaluationTest`, `TestTurtleEval`. */
  type: string
  name: string
  /** Syntax and RDF tests: the input file. */
  action?: string
  query?: string
  request?: string
  data: string[]
  graphData: GraphFile[]
  /** An expected-result file, or an expected dataset (update tests). */
  result?: string | { data: string[]; graphData: GraphFile[] }
  approval?: string
}

const FORMATS: Record<string, RdfFormat> = { ttl: 'turtle', nt: 'ntriples', nq: 'nquads', trig: 'trig', rdf: 'rdfxml' }

export const localPath = (iri: string) => {
  if (!iri.startsWith(ROOT)) throw new Error(`not a test-suite IRI: ${iri}`)
  return TESTS_DIR + iri.slice(ROOT.length)
}
export const readText = (iri: string) => readFileSync(localPath(iri), 'utf8')

export function rdfFile(iri: string, format?: RdfFormat): RdfFile {
  const ext = iri.slice(iri.lastIndexOf('.') + 1)
  const f = format ?? FORMATS[ext]
  if (!f) throw new Error(`no RDF format for ${iri}`)
  return { text: readText(iri), format: f, base: iri }
}

class Graph {
  private readonly bySubject = new Map<string, Row[]>()
  constructor(quads: Row[]) {
    for (const q of quads) {
      const k = key(q[0])
      let list = this.bySubject.get(k)
      if (!list) this.bySubject.set(k, (list = []))
      list.push(q)
    }
  }
  all(s: T, p: string): T[] {
    return (this.bySubject.get(key(s)) ?? []).filter((q) => (q[1] as { v: string }).v === p).map((q) => q[2]!)
  }
  one(s: T, p: string): T | undefined {
    return this.all(s, p)[0]
  }
  list(head: T | undefined): T[] {
    const out: T[] = []
    while (head && !(head.t === 'iri' && head.v === RDF + 'nil')) {
      out.push(this.one(head, RDF + 'first')!)
      head = this.one(head, RDF + 'rest')
    }
    return out
  }
}

const v = (t: T | undefined) => (t && 'v' in t ? t.v : undefined)
const local = (iri: string) => iri.slice(Math.max(iri.lastIndexOf('#'), iri.lastIndexOf('/')) + 1)

/** Every test reachable from a manifest (relative to the rdf-tests root), following mf:include. */
export function loadManifest(path: string, seen = new Set<string>()): Test[] {
  const iri = ROOT + path
  if (seen.has(iri)) return []
  seen.add(iri)
  const g = new Graph(oxParse(rdfFile(iri, 'turtle')))
  const manifests = g.all({ t: 'iri', v: iri }, RDF + 'type').length
    ? [{ t: 'iri', v: iri } as T]
    : [] // a manifest node other than <> (e.g. `trs:manifest`): find it by type below
  if (!manifests.length) {
    for (const q of oxParse(rdfFile(iri, 'turtle'))) {
      if (v(q[1]) === RDF + 'type' && v(q[2]) === MF + 'Manifest') manifests.push(q[0]!)
    }
  }
  const tests: Test[] = []
  for (const m of manifests) {
    for (const inc of g.list(g.one(m, MF + 'include'))) tests.push(...loadManifest(v(inc)!.slice(ROOT.length), seen))
    for (const e of g.list(g.one(m, MF + 'entries'))) tests.push(entry(g, e))
  }
  return tests
}

function graphFiles(g: Graph, s: T, p: string): GraphFile[] {
  return g.all(s, p).map((x) =>
    x.t === 'iri' ? { name: x.v, file: x.v } : { name: v(g.one(x, RDFS + 'label'))!, file: v(g.one(x, UT + 'graph'))! },
  )
}

function entry(g: Graph, e: T): Test {
  const type = v(g.all(e, RDF + 'type').find((t) => !v(t)!.endsWith('#Test')))!
  const test: Test = {
    id: v(e)!,
    type: local(type),
    name: v(g.one(e, MF + 'name')) ?? '',
    data: [],
    graphData: [],
    approval: v(g.one(e, DAWGT + 'approval')) && local(v(g.one(e, DAWGT + 'approval'))!),
  }
  const action = g.one(e, MF + 'action')
  if (action?.t === 'iri') test.action = action.v
  else if (action) {
    test.query = v(g.one(action, QT + 'query'))
    test.request = v(g.one(action, UT + 'request'))
    test.data = [...g.all(action, QT + 'data'), ...g.all(action, UT + 'data')].map((x) => v(x)!)
    test.graphData = [...graphFiles(g, action, QT + 'graphData'), ...graphFiles(g, action, UT + 'graphData')]
  }
  const result = g.one(e, MF + 'result')
  if (result?.t === 'iri') test.result = result.v
  else if (result) test.result = { data: g.all(result, UT + 'data').map((x) => v(x)!), graphData: graphFiles(g, result, UT + 'graphData') }
  return test
}
