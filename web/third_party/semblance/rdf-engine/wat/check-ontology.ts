// Loads Semblance's ontology and demo/app.ttl through the host adapter and
// compares each file's quads with Oxigraph's (counts, then the quads up to
// blank node renaming), and times the whole load.
// `npx tsx rdf-engine/wat/check-ontology.ts [wat|rust]`
import { readFileSync } from 'node:fs'
import * as ox from 'oxigraph'
import { ONTOLOGY_FILES } from '../../src/ontology.ts'
import { Engine, Format, Kind } from '../host/engine.ts'
import { quadRow, isomorphic, iri, lit, rowKey, type Row, type T } from '../conformance/terms.ts'

const root = new URL('../../', import.meta.url)
const name = process.argv[2] ?? 'wat'
const module = new WebAssembly.Module(readFileSync(new URL(`../build/${name}.wasm`, import.meta.url)))
const files = [...ONTOLOGY_FILES.map((f) => 'ontology/' + f), 'demo/app.ttl'].map((f) => [f, readFileSync(new URL(f, root), 'utf8')] as const)

function term(e: Engine, id: number): T {
  switch (e.kind(id)) {
    case Kind.Iri:
      return iri(e.value(id))
    case Kind.BlankNode:
      return { t: 'bnode', v: e.value(id) }
    case Kind.Literal: {
      const d = e.direction(id)
      return lit(e.value(id), e.value(e.datatype(id)), e.lang(id) || undefined, d === 1 ? 'ltr' : d === 2 ? 'rtl' : undefined)
    }
    case Kind.Triple: {
      const [s, p, o] = e.tripleParts(id)
      return { t: 'triple', s: term(e, s), p: term(e, p), o: term(e, o) }
    }
    default:
      return { t: 'default' }
  }
}

let bad = 0
let total = 0
for (const [file, text] of files) {
  const e = new Engine(module)
  let rows: Row[] = []
  try {
    e.load(Format.Turtle, text)
    const ids = e.match()
    for (let i = 0; i < ids.length; i += 4) rows.push([0, 1, 2, 3].map((k) => term(e, ids[i + k]!)))
  } catch (err) {
    console.log(`FAIL ${file}: ${(err as Error).message}`)
    bad++
    continue
  }
  // Document order on both sides keeps the blank node matching cheap.
  const want = ox.parse(text, { format: 'text/turtle' }).map(quadRow)
  total += rows.length
  // Large files can exceed the matcher's recursion; then compare with blank nodes blanked out.
  const ground = (rs: Row[]) => rs.map((r) => rowKey(r, true)).sort().join('\n')
  let same: boolean | undefined
  try {
    same = rows.length === want.length && isomorphic(rows, want, false)
  } catch {
    same = ground(rows) === ground(want)
  }
  if (same !== true) {
    console.log(`FAIL ${file}: ${rows.length} quads, Oxigraph ${want.length}${rows.length === want.length ? ` (different quads: ${same})` : ''}`)
    bad++
  }
}

const t = performance.now()
const all = new Engine(module)
for (const [, text] of files) all.load(Format.Turtle, text)
const ms = performance.now() - t
console.log(`${name}: ${files.length - bad}/${files.length} files match Oxigraph, ${total} quads; all loaded into one store in ${ms.toFixed(1)} ms`)
process.exitCode = bad ? 1 : 0
