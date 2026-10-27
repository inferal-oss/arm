// Semblance's GraphStore over any engine build. Terms cross the boundary once:
// the adapter's own (shared) term objects carry their engine id, ids map back
// to them, and the factory hands out those same objects. Terms from other
// factories are looked up by value. In idle time (`maintain`) the engine frees
// terms nothing uses any more; the adapter then forgets their ids.
import type { DefaultGraph, Literal, NamedNode, Quad, Term } from '@rdfjs/types'
import type { Factory, GraphStore } from '../../src/store/types.ts'
import { DEFAULT_GRAPH, Engine, EngineError, Form, Format, Kind, type Query } from './engine.ts'

const XSD_STRING = 'http://www.w3.org/2001/XMLSchema#string'

/** Engine ids are tagged on term objects under a key of each store's own. */
type Tagged = { [key: symbol]: number | undefined }

abstract class Base {
  [key: symbol]: number | undefined
  abstract readonly termType: string
  abstract readonly value: string
  equals(other: Term | null | undefined): boolean {
    if (!other || other.termType !== this.termType || other.value !== this.value) return false
    if (this.termType !== 'Literal') return true
    const a = this as unknown as Literal
    const b = other as Literal
    return a.language === b.language && (a.direction ?? '') === (b.direction ?? '') && a.datatype.value === b.datatype.value
  }
}

class Iri extends Base implements NamedNode {
  readonly termType = 'NamedNode' as const
  constructor(readonly value: string) {
    super()
  }
}

class Blank extends Base {
  readonly termType = 'BlankNode' as const
  constructor(readonly value: string) {
    super()
  }
}

class Lit extends Base implements Literal {
  readonly termType = 'Literal' as const
  constructor(
    readonly value: string,
    readonly language: string,
    readonly direction: '' | 'ltr' | 'rtl',
    readonly datatype: NamedNode,
  ) {
    super()
  }
}

class Default extends Base implements DefaultGraph {
  readonly termType = 'DefaultGraph' as const
  readonly value = '' as const
}

class TripleTerm extends Base {
  readonly termType = 'Quad' as const
  readonly value = ''
  constructor(
    readonly subject: Term,
    readonly predicate: Term,
    readonly object: Term,
    readonly graph: Term,
  ) {
    super()
  }
  override equals(other: Term | null | undefined): boolean {
    const q = other as Quad | undefined
    return !!q && q.termType === 'Quad' && q.subject.equals(this.subject) && q.predicate.equals(this.predicate) && q.object.equals(this.object) && q.graph.equals(this.graph)
  }
}

/** A store quad; distinct from triple terms only in being used as a statement. */
const quad = (s: Term, p: Term, o: Term, g: Term) => new TripleTerm(s, p, o, g) as unknown as Quad

/** A GraphStore with the engine's own counters, for tests and benchmarks. */
export type EngineStore = GraphStore & {
  termCount(): number
  /** A deadline for queries, on the Date.now() clock: one running past it throws an error with `interrupted` (undefined: none). */
  setDeadline(at: number | undefined): void
  /** A step budget for each query, as setDeadline (undefined: unlimited). */
  setFuel(steps: number | undefined): void
  /** The engine itself and term ids, for measurements that bypass the adapter. */
  engine: Engine
  idOf(t: Term): number
}

export interface EngineStoreOptions {
  /**
   * Compiled queries (ABI.md, "Optional: compiled queries"): compile a prepared
   * query, for the variables it has bound, on its n-th run with them. 0: never.
   * 'idle': in idle time (`maintain`), the queries whose timed runs say it pays
   * (Engine.compileIdle).
   */
  compile?: number | 'idle'
}

/** Called with every engine store made (benchmarks use it to watch queries). */
export const engineStoreHooks: { created?: (store: EngineStore) => void } = {}

export function engineStore(module: WebAssembly.Module, kind: string, options: EngineStoreOptions = {}): EngineStore {
  const e = new Engine(module)
  e.autoCompile = typeof options.compile === 'number' ? options.compile : 0
  e.idleCompile = options.compile === 'idle'
  // This store's tag: term objects of another store (another engine) carry ids that mean nothing here.
  const ID = Symbol('engine id')
  const defaultGraph = new Default()
  defaultGraph[ID] = DEFAULT_GRAPH
  /** Term objects by id, filled as ids come back from the engine. */
  const byId: (Term | undefined)[] = [undefined, defaultGraph]
  /** Shared term objects by value, so the factory and results agree. */
  const iris = new Map<string, Iri>()
  const xsdString = namedNode(XSD_STRING)

  function namedNode(value: string): Iri {
    let t = iris.get(value)
    if (!t) iris.set(value, (t = new Iri(value)))
    return t
  }

  const literals = new Map<string, Lit>()
  const literalKey = (value: string, language: string, direction: string, datatype: string) => value + '\u0000' + (language ? language + '\u0000' + direction : datatype)
  function sharedLiteral(value: string, language: string, direction: '' | 'ltr' | 'rtl', datatype: string): Lit {
    const k = literalKey(value, language, direction, datatype)
    let t = literals.get(k)
    if (!t) {
      const dt = language ? (direction ? RDF_DIR_LANG : RDF_LANG) : datatype
      literals.set(k, (t = new Lit(value, language, direction, namedNode(dt))))
    }
    return t
  }

  function literal(value: string, languageOrDatatype?: string | NamedNode | { language: string; direction?: string }): Literal {
    if (typeof languageOrDatatype === 'string') return sharedLiteral(value, languageOrDatatype.toLowerCase(), '', '')
    if (languageOrDatatype && 'language' in languageOrDatatype) {
      return sharedLiteral(value, languageOrDatatype.language.toLowerCase(), (languageOrDatatype.direction ?? '') as '' | 'ltr' | 'rtl', '')
    }
    return sharedLiteral(value, '', '', languageOrDatatype?.value ?? XSD_STRING)
  }

  const bnodes = new Map<string, Blank>()
  function blankNode(label: string): Blank {
    let t = bnodes.get(label)
    if (!t) bnodes.set(label, (t = new Blank(label)))
    return t
  }

  /** The engine id of a term: interned when `intern`, else 0 if the engine has never seen it. */
  function idOf(t: Term, intern: boolean): number {
    const tagged = t as Term & Tagged
    if (tagged[ID] !== undefined) return tagged[ID]
    let id: number
    switch (t.termType) {
      case 'NamedNode': {
        const shared = namedNode(t.value)
        if (shared[ID] !== undefined) return shared[ID]
        id = intern ? e.iri(t.value) : e.lookupIri(t.value)
        if (id) {
          shared[ID] = id
          byId[id] ??= shared
        }
        break
      }
      case 'DefaultGraph':
        return DEFAULT_GRAPH
      case 'BlankNode': {
        const shared = blankNode(t.value)
        if (shared[ID] === undefined) {
          shared[ID] = e.bnode(t.value)
          byId[shared[ID]] ??= shared
        }
        return shared[ID]
      }
      case 'Literal': {
        const l = t as Literal
        const lang = l.language.toLowerCase()
        const direction = (l.direction ?? '') as '' | 'ltr' | 'rtl'
        const shared = sharedLiteral(l.value, lang, direction, l.datatype.value)
        if (shared[ID] === undefined) {
          const dir = direction === 'ltr' ? 1 : direction === 'rtl' ? 2 : 0
          shared[ID] = lang ? e.langLiteral(l.value, lang, dir) : e.literal(l.value, l.datatype.value === XSD_STRING ? 0 : idOf(l.datatype, true))
          byId[shared[ID]] ??= shared
        }
        return shared[ID]
      }
      case 'Quad': {
        const q = t as unknown as Quad
        const [s, p, o] = [idOf(q.subject, intern), idOf(q.predicate, intern), idOf(q.object, intern)]
        if (!s || !p || !o) return 0
        id = e.triple(s, p, o)
        break
      }
      default:
        throw new Error(`cannot store a ${t.termType}`)
    }
    if (id && t instanceof Base) {
      tagged[ID] = id
      byId[id] ??= t
    }
    return id
  }

  function termOf(id: number): Term {
    const known = byId[id]
    if (known) return known
    let t: Base
    switch (e.kind(id)) {
      case Kind.Iri:
        t = namedNode(e.value(id))
        break
      case Kind.BlankNode:
        t = blankNode(e.value(id))
        break
      case Kind.Literal: {
        const dir = e.direction(id)
        t = sharedLiteral(e.value(id), e.lang(id), dir === 1 ? 'ltr' : dir === 2 ? 'rtl' : '', termOf(e.datatype(id)).value)
        break
      }
      case Kind.Triple: {
        const [s, p, o] = e.tripleParts(id)
        t = new TripleTerm(termOf(s), termOf(p), termOf(o), defaultGraph)
        break
      }
      default:
        throw new Error(`engine returned unknown term id ${id}`)
    }
    t[ID] = id
    byId[id] = t as Term
    return t as Term
  }

  // Parsed queries by text. Semblance substitutes each focus into the text, so
  // this is bounded; prepared queries with bound variables avoid the parse.
  const prepared = new Map<string, Query>()
  const prepare = (text: string) => {
    let q = prepared.get(text)
    if (q) {
      prepared.delete(text)
    } else {
      q = e.prepare(text)
      if (prepared.size >= 512) {
        const [oldest, old] = prepared.entries().next().value!
        prepared.delete(oldest)
        old.release()
      }
    }
    prepared.set(text, q)
    return q
  }
  /**
   * IRIs the engine did not know when looked up. Every term a quad is added with is tagged
   * first, so an IRI that is still untagged is in no quad, unless Turtle loaded since: for
   * matching (and graph lists) it stays unknown without asking the engine again.
   */
  const unknown = new Set<string>()
  /** The engine id of a term to find quads by, or 0 if no quad can contain it. */
  function known(t: Term): number {
    if (t.termType !== 'NamedNode' || (t as Term & Tagged)[ID] !== undefined) return idOf(t, false)
    const shared = namedNode(t.value)
    if (shared[ID] !== undefined) return shared[ID]
    if (unknown.has(t.value)) return 0
    const id = idOf(t, false)
    if (!id) {
      if (unknown.size >= 4096) unknown.clear()
      unknown.add(t.value)
    }
    return id
  }

  const graphIds = (graphs: Term[]) => {
    const out: number[] = []
    for (const g of graphs) {
      const id = known(g)
      if (id) out.push(id)
    }
    // Every requested graph unknown to the engine: an empty default graph.
    return graphs.length && !out.length ? [0xffffffff] : out
  }
  let sparql: boolean
  try {
    e.prepare('ASK {}').release()
    sparql = true
  } catch (err) {
    if (!(err instanceof EngineError)) throw err
    sparql = false
  }

  const triplesOf = (r: ReturnType<Query['run']>): Quad[] => {
    if (r.form !== Form.Construct && r.form !== Form.Describe) throw new Error('not a CONSTRUCT query')
    const out: Quad[] = new Array(r.triples.length / 3)
    for (let i = 0, k = 0; i < r.triples.length; i += 3, k++) out[k] = quad(termOf(r.triples[i]!), termOf(r.triples[i + 1]!), termOf(r.triples[i + 2]!), defaultGraph)
    return out
  }

  /** Free unused engine terms and forget them here. */
  let liveAfterCollect = e.termCount()
  function collect() {
    for (const id of e.collect()) {
      const t = byId[id] as (Base & Term) | undefined
      if (!t) continue
      byId[id] = undefined
      delete t[ID]
      if (t instanceof Iri) iris.delete(t.value)
      else if (t instanceof Blank) bnodes.delete(t.value)
      else if (t instanceof Lit) literals.delete(literalKey(t.value, t.language, t.direction, t.datatype.value))
    }
    liveAfterCollect = e.termCount()
  }

  const factory: Factory = {
    namedNode,
    literal: literal as Factory['literal'],
    defaultGraph: () => defaultGraph,
    quad: (s, p, o, g) => quad(s, p, o, g ?? defaultGraph),
  }

  const store: EngineStore = {
    kind,
    factory,
    sparql,
    crossGraphMatch: true,
    add(q) {
      e.add(idOf(q.subject, true), idOf(q.predicate, true), idOf(q.object, true), idOf(q.graph, true))
    },
    delete(q) {
      const s = known(q.subject)
      const p = known(q.predicate)
      const o = known(q.object)
      const g = known(q.graph)
      if (s && p && o && g) e.remove(s, p, o, g)
    },
    has(q) {
      const s = known(q.subject)
      const p = known(q.predicate)
      const o = known(q.object)
      const g = known(q.graph)
      return !!(s && p && o && g) && e.has(s, p, o, g)
    },
    match(s, p, o, g) {
      const a = s ? known(s) : 0
      const b = p ? known(p) : 0
      const c = o ? known(o) : 0
      const d = g ? known(g) : 0
      // A bound term the engine has never seen matches nothing (0 would mean "any").
      if ((s && !a) || (p && !b) || (o && !c) || (g && !d)) return []
      const r = e.match(a, b, c, d)
      const out: Quad[] = new Array(r.length / 4)
      for (let i = 0, k = 0; i < r.length; i += 4, k++) out[k] = quad(termOf(r[i]!), termOf(r[i + 1]!), termOf(r[i + 2]!), termOf(r[i + 3]!))
      return out
    },
    select(query, graphs) {
      if (!sparql) throw new Error(`The ${kind} store has no SPARQL engine yet`)
      const r = prepare(query).run(graphIds(graphs as Term[]))
      if (r.form !== Form.Select) throw new Error('not a SELECT query')
      const out: Map<string, Term>[] = []
      const width = r.vars.length
      if (!width) return Array.from({ length: r.count }, () => new Map<string, Term>())
      for (let i = 0; i < r.rows.length; i += width) {
        const m = new Map<string, Term>()
        for (let k = 0; k < width; k++) if (r.rows[i + k]) m.set(r.vars[k]!, termOf(r.rows[i + k]!))
        out.push(m)
      }
      return out
    },
    constructFor(query, variable, values, graphs) {
      const q = prepare(query)
      const ids: number[] = []
      for (const v of values) {
        const id = idOf(v, false)
        if (id) ids.push(id)
      }
      if (!ids.length) return []
      q.bindValues(variable, ids)
      try {
        return triplesOf(q.run(graphIds(graphs as Term[])))
      } finally {
        q.bindValues(variable, [])
      }
    },
    construct(query, graphs) {
      if (!sparql) throw new Error(`The ${kind} store has no SPARQL engine yet; use function rules (prefer: "function")`)
      return triplesOf(prepare(query).run(graphIds(graphs as Term[])))
    },
    loadTurtle(text) {
      unknown.clear()
      e.load(Format.Turtle, text)
    },
    termCount: () => e.termCount(),
    dispose() {
      // Each store has its own instance and memory; drop them and the term caches,
      // so they go as soon as the engine object does, not when the store's last holder does.
      prepared.clear()
      byId.length = 0
      iris.clear()
      literals.clear()
      unknown.clear()
      e.dispose()
    },
    setDeadline: (at) => e.setDeadline(at),
    setFuel: (steps) => e.setFuel(steps),
    engine: e,
    idOf: (t) => idOf(t, false),
    maintain(budgetMs) {
      const t0 = performance.now()
      // Collect once the dictionary has grown by half since the last collection.
      if (e.termCount() > liveAfterCollect * 1.5 + 1000) collect()
      // What time is left compiles the queries that have run most (compiled: 'idle').
      return e.compileIdle(budgetMs - (performance.now() - t0))
    },
  }
  engineStoreHooks.created?.(store)
  return store
}

const RDF_LANG = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#langString'
const RDF_DIR_LANG = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#dirLangString'
