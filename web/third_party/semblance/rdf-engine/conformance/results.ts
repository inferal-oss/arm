// Readers for expected query results: SPARQL results JSON (.srj), XML (.srx),
// and the DAWG result-set vocabulary in RDF (.ttl, .rdf).
import { DOMParser } from 'linkedom'
import type { QueryResult } from './target.ts'
import { iri, key, lit, type Row, type T } from './terms.ts'

interface JsonTerm {
  type: string
  value: string | { subject: JsonTerm; predicate: JsonTerm; object: JsonTerm }
  datatype?: string
  'xml:lang'?: string
  'its:dir'?: string
}

function jsonTerm(j: JsonTerm): T {
  if (j.type === 'triple') {
    const v = j.value as { subject: JsonTerm; predicate: JsonTerm; object: JsonTerm }
    return { t: 'triple', s: jsonTerm(v.subject), p: jsonTerm(v.predicate), o: jsonTerm(v.object) }
  }
  const v = j.value as string
  if (j.type === 'uri') return iri(v)
  if (j.type === 'bnode') return { t: 'bnode', v }
  if (j.type === 'literal' || j.type === 'typed-literal') return lit(v, j.datatype, j['xml:lang'], j['its:dir'])
  throw new Error(`unknown SPARQL JSON term type ${j.type}`)
}

export function parseSrj(text: string): QueryResult {
  const doc = JSON.parse(text) as { head?: { vars?: string[] }; boolean?: boolean; results?: { bindings: Record<string, JsonTerm>[] } }
  if (typeof doc.boolean === 'boolean') return { kind: 'boolean', value: doc.boolean }
  return {
    kind: 'bindings',
    vars: doc.head?.vars ?? [],
    rows: (doc.results?.bindings ?? []).map((b) => new Map(Object.entries(b).map(([k, v]) => [k, jsonTerm(v)]))),
  }
}

type El = { localName: string; children: ArrayLike<El>; textContent: string | null; getAttribute(n: string): string | null }
const kids = (e: El, name?: string) => Array.from(e.children).filter((c) => !name || c.localName === name)

function xmlTerm(e: El): T {
  switch (e.localName) {
    case 'uri':
      return iri(e.textContent!.trim())
    case 'bnode':
      return { t: 'bnode', v: e.textContent!.trim() }
    case 'literal':
      return lit(e.textContent ?? '', e.getAttribute('datatype') ?? undefined, e.getAttribute('xml:lang') ?? undefined, e.getAttribute('its:dir') ?? undefined)
    case 'triple': {
      const part = (n: string) => xmlTerm(kids(kids(e, n)[0]!)[0]!)
      return { t: 'triple', s: part('subject'), p: part('predicate'), o: part('object') }
    }
    default:
      throw new Error(`unknown SPARQL XML term <${e.localName}>`)
  }
}

export function parseSrx(text: string): QueryResult {
  const doc = new DOMParser().parseFromString(text, 'text/xml') as unknown as { documentElement: El }
  const root = doc.documentElement
  const bool = kids(root, 'boolean')[0]
  if (bool) return { kind: 'boolean', value: bool.textContent!.trim() === 'true' }
  const vars = kids(kids(root, 'head')[0]!, 'variable').map((v) => v.getAttribute('name')!)
  const rows = kids(kids(root, 'results')[0] ?? ({ children: [] } as unknown as El), 'result').map(
    (r) => new Map(kids(r, 'binding').map((b) => [b.getAttribute('name')!, xmlTerm(kids(b)[0]!)])),
  )
  return { kind: 'bindings', vars, rows }
}

const RS = 'http://www.w3.org/2001/sw/DataAccess/tests/result-set#'
const RDF_TYPE = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#type'

/**
 * A result set written in the DAWG result-set vocabulary, or `undefined` when
 * the graph holds no rs:ResultSet (it is then an expected CONSTRUCT graph).
 */
export function fromResultGraph(quads: Row[]): QueryResult | undefined {
  const out = (s: T, p: string) => quads.filter((q) => key(q[0]) === key(s) && q[1]!.t === 'iri' && q[1]!.v === p).map((q) => q[2]!)
  const set = quads.find((q) => q[1]!.t === 'iri' && q[1]!.v === RDF_TYPE && q[2]!.t === 'iri' && q[2]!.v === RS + 'ResultSet')?.[0]
  if (!set) return undefined
  const bool = out(set, RS + 'boolean')[0]
  if (bool) return { kind: 'boolean', value: bool.t === 'lit' && bool.v === 'true' }
  const vars = out(set, RS + 'resultVariable').map((v) => (v as { v: string }).v)
  const solutions = out(set, RS + 'solution').map((s) => {
    const index = out(s, RS + 'index')[0]
    const row = new Map<string, T>()
    for (const b of out(s, RS + 'binding')) row.set((out(b, RS + 'variable')[0] as { v: string }).v, out(b, RS + 'value')[0]!)
    return { index: index ? Number((index as { v: string }).v) : 0, row }
  })
  solutions.sort((x, y) => x.index - y.index)
  return { kind: 'bindings', vars, rows: solutions.map((s) => s.row) }
}
