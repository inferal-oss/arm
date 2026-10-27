// Checks an engine build against the term and store parts of ABI.md:
// `npx tsx rdf-engine/host/abi-check.ts rust|wat`
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { DEFAULT_GRAPH, Engine, EngineError, Format, Kind } from './engine.ts'

const XSD = 'http://www.w3.org/2001/XMLSchema#'
const RDF = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#'

const name = process.argv[2] ?? 'rust'
const module = new WebAssembly.Module(readFileSync(new URL(`../build/${name}.wasm`, import.meta.url)))
const checks: [string, (e: Engine) => void][] = []
const check = (label: string, f: (e: Engine) => void) => checks.push([label, f])

check('terms intern to stable ids', (e) => {
  const a = e.iri('http://example/a')
  assert.ok(a > 1)
  assert.equal(e.iri('http://example/a'), a)
  assert.notEqual(e.iri('http://example/b'), a)
  assert.equal(e.kind(a), Kind.Iri)
  assert.equal(e.value(a), 'http://example/a')
  assert.equal(e.kind(DEFAULT_GRAPH), Kind.DefaultGraph)
  assert.equal(e.lookupIri('http://example/never'), 0)
  assert.equal(e.lookupIri('http://example/a'), a)
})

check('kinds are distinct namespaces', (e) => {
  const v = 'x'
  const ids = new Set([e.iri(v), e.bnode(v), e.literal(v)])
  assert.equal(ids.size, 3)
})

check('literals', (e) => {
  const s = e.literal('hé 🙂')
  assert.equal(e.value(s), 'hé 🙂')
  assert.equal(e.value(e.datatype(s)), XSD + 'string')
  assert.equal(e.literal('hé 🙂', e.iri(XSD + 'string')), s, 'datatype 0 is xsd:string')
  const i = e.literal('01', e.iri(XSD + 'integer'))
  assert.notEqual(i, e.literal('1', e.iri(XSD + 'integer')), 'lexical forms are kept')
  const l = e.langLiteral('chat', 'FR')
  assert.equal(e.langLiteral('chat', 'fr'), l)
  assert.equal(e.lang(l), 'fr')
  assert.equal(e.value(e.datatype(l)), RDF + 'langString')
  assert.equal(e.direction(l), 0)
  const d = e.langLiteral('chat', 'fr', 2)
  assert.notEqual(d, l)
  assert.equal(e.direction(d), 2)
  assert.equal(e.value(e.datatype(d)), RDF + 'dirLangString')
  assert.equal(e.lang(s), '')
})

check('blank nodes', (e) => {
  const b = e.bnode('b0')
  assert.equal(e.bnode('b0'), b)
  assert.equal(e.kind(b), Kind.BlankNode)
  const f = e.bnode()
  assert.notEqual(f, e.bnode())
  assert.notEqual(f, b)
})

check('triple terms', (e) => {
  const [s, p, o] = [e.iri('http://s'), e.iri('http://p'), e.literal('o')]
  const t = e.triple(s, p, o)
  assert.equal(e.triple(s, p, o), t)
  assert.equal(e.kind(t), Kind.Triple)
  assert.deepEqual(e.tripleParts(t), [s, p, o])
  const nested = e.triple(t, p, t)
  assert.deepEqual(e.tripleParts(nested), [t, p, t])
})

check('add, has, remove', (e) => {
  const [s, p, o, g] = ['s', 'p', 'o', 'g'].map((x) => e.iri('http://example/' + x)) as [number, number, number, number]
  assert.equal(e.add(s, p, o), true)
  assert.equal(e.add(s, p, o), false)
  assert.equal(e.has(s, p, o), true)
  assert.equal(e.has(s, p, o, g), false)
  assert.equal(e.add(s, p, o, g), true)
  assert.equal(e.quadCount(), 2)
  assert.equal(e.remove(s, p, o), true)
  assert.equal(e.remove(s, p, o), false)
  assert.equal(e.has(s, p, o), false)
  assert.equal(e.has(s, p, o, g), true)
  assert.equal(e.quadCount(), 1)
})

check('match agrees with a brute-force scan', (e) => {
  let seed = 7
  const rand = (n: number) => ((seed = (Math.imul(seed, 1103515245) + 12345) & 0x7fffffff), (seed >>> 8) % n)
  const terms = Array.from({ length: 12 }, (_, i) => e.iri('http://example/t' + i))
  const graphs = [DEFAULT_GRAPH, terms[10]!, terms[11]!]
  const quads = new Map<string, number[]>()
  for (let i = 0; i < 3000; i++) {
    const q = [terms[rand(6)]!, terms[rand(4)]!, terms[rand(8)]!, graphs[rand(3)]!]
    if (rand(4) === 0) {
      e.remove(q[0]!, q[1]!, q[2]!, q[3]!)
      quads.delete(q.join())
    } else {
      e.add(q[0]!, q[1]!, q[2]!, q[3]!)
      quads.set(q.join(), q)
    }
  }
  assert.equal(e.quadCount(), quads.size)
  const sorted = (ids: Uint32Array) => {
    const out: string[] = []
    for (let i = 0; i < ids.length; i += 4) out.push([ids[i], ids[i + 1], ids[i + 2], ids[i + 3]].join())
    return out.sort()
  }
  for (let i = 0; i < 400; i++) {
    const pick = (pool: number[]) => (rand(2) ? pool[rand(pool.length)]! : 0)
    const pat = [pick(terms.slice(0, 6)), pick(terms.slice(0, 4)), pick(terms.slice(0, 8)), rand(2) ? 0 : graphs[rand(3)]!]
    const want = [...quads.values()].filter((q) => pat.every((x, k) => !x || x === q[k])).map((q) => q.join()).sort()
    assert.deepEqual(sorted(e.match(pat[0], pat[1], pat[2], pat[3])), want, `match(${pat})`)
  }
})

check('apply removes, then adds', (e) => {
  const [a, b, c] = ['a', 'b', 'c'].map((x) => e.iri('http://example/' + x)) as [number, number, number]
  e.add(a, b, c)
  e.apply([a, b, c, DEFAULT_GRAPH, b, b, b, DEFAULT_GRAPH], [c, c, c, DEFAULT_GRAPH, a, b, c, DEFAULT_GRAPH])
  assert.equal(e.quadCount(), 2)
  assert.equal(e.has(c, c, c), true)
  assert.equal(e.has(a, b, c), true, 'a quad both removed and added is present')
})

check('load scopes blank nodes per document and is all or nothing', (e) => {
  const doc = '_:x <http://example/p> "1" .\n'
  assert.equal(e.load(Format.NTriples, doc), 1)
  assert.equal(e.load(Format.NTriples, doc), 1)
  assert.equal(e.quadCount(), 2, 'each document has its own _:x')
  const g = e.iri('http://example/g')
  assert.equal(e.load(Format.NTriples, '<http://example/s> <http://example/p> <http://example/o> .', '', g), 1)
  assert.equal(e.match(0, 0, 0, g).length, 4)
  assert.equal(e.load(Format.NQuads, '<http://example/s> <http://example/p> <http://example/o> <http://example/h> .'), 1)
  assert.equal(e.match(0, 0, 0, e.iri('http://example/h')).length, 4)
  assert.throws(() => e.load(Format.NTriples, '<http://example/s> <http://example/p> <http://example/o2> .\n<oops'), EngineError)
  assert.equal(e.quadCount(), 4)
})

check('collect frees unused terms and keeps the rest', (e) => {
  const [s, p] = [e.iri('http://example/s'), e.iri('http://example/p')]
  const o = e.literal('kept', e.iri('http://example/dt'))
  const t = e.triple(s, p, e.literal('inside'))
  e.add(s, p, o)
  e.add(t, p, s, e.iri('http://example/g'))
  const garbage = [e.iri('http://example/unused'), e.literal('unused'), e.langLiteral('x', 'en'), e.bnode('b')]
  const before = e.termCount()
  const freed = new Set(e.collect())
  for (const id of garbage) assert.ok(freed.has(id), `id ${id} freed`)
  for (const id of [s, p, o, t]) assert.ok(!freed.has(id), `id ${id} kept`)
  assert.equal(e.termCount(), before - freed.size)
  assert.equal(e.value(o), 'kept')
  assert.equal(e.value(e.datatype(o)), 'http://example/dt', "a literal's datatype is kept")
  assert.equal(e.value(e.tripleParts(t)[2]), 'inside', "a triple term's parts are kept")
  assert.equal(e.lookupIri('http://example/s'), s, 'lookups work after collect')
  assert.equal(e.lookupIri('http://example/unused'), 0)
  assert.equal(e.match(s, p).length, 4)
  const reused = e.iri('http://example/new')
  assert.ok(freed.has(reused), 'freed ids are reused')
  assert.equal(e.value(reused), 'http://example/new')
  assert.equal(e.kind(DEFAULT_GRAPH), Kind.DefaultGraph)
})

let failed = 0
for (const [label, f] of checks) {
  try {
    f(new Engine(module))
    console.log(`ok   ${label}`)
  } catch (err) {
    failed++
    console.log(`FAIL ${label}\n     ${(err as Error).message.split('\n').join('\n     ')}`)
  }
}
console.log(`\n${name}: ${checks.length - failed}/${checks.length} ABI checks pass`)
process.exitCode = failed ? 1 : 0
