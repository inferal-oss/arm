import type { Quad, Quad_Graph, Term } from '@rdfjs/types'
import type * as Ox from 'oxigraph'
import { DataFactory } from 'n3'
import type { Factory, GraphStore } from './types.ts'

const COMPACT_MIN = 5_000

/**
 * Oxigraph adapter. The module is injected because Node (`oxigraph/node.js`)
 * and browsers (`oxigraph/web.js`, needs `init()`) load it differently.
 */
export function oxigraphStore(ox: typeof Ox): GraphStore {
  let store = new ox.Store()
  // Terms the engine makes are plain RDF/JS objects (N3's), which Oxigraph accepts: each of its
  // own terms is a WASM object registered with a FinalizationRegistry, and making millions of them
  // (every namedNode() of every rule) left V8 a backlog of weak cells that made every collection,
  // and so every turn, slower the longer a page or a test process ran.
  const factory = DataFactory as unknown as Factory
  // Oxigraph's in-memory store keeps deleted quads as dead versions that every
  // later lookup of the same pattern walks, so a sliding window (a bounded log,
  // replaced metrics) gets slower forever. Rebuild from the live quads once the
  // deletions since the last rebuild reach half of them: amortized O(1) per write.
  // (The hybrid store rebuilds in idle slices instead; see hybrid.ts.)
  let deletes = 0
  let disposed = false
  let threshold = COMPACT_MIN
  const compact = () => {
    const live = store.match(null, null, null, null)
    const fresh = new ox.Store(live)
    ;(store as unknown as { free?: () => void }).free?.()
    store = fresh
    deletes = 0
    threshold = Math.max(COMPACT_MIN, live.length / 2)
  }
  return {
    kind: 'oxigraph',
    factory,
    sparql: true,
    crossGraphMatch: true,
    add: (q) => store.add(q as Ox.Quad),
    delete: (q) => {
      store.delete(q as Ox.Quad)
      if (++deletes > threshold) compact()
    },
    has: (q) => store.has(q as Ox.Quad),
    match: (s, p, o, g) => store.match(s as Ox.Term, p as Ox.Term, o as Ox.Term, g as Ox.Term) as unknown as Quad[],
    select(query: string, graphs: Quad_Graph[]) {
      return store.query(query, { default_graph: graphs as Ox.NamedNode[] }) as unknown as Map<string, Term>[]
    },
    construct(query: string, graphs: Quad_Graph[]): Quad[] {
      return store.query(query, { default_graph: graphs as Ox.NamedNode[] }) as unknown as Quad[]
    },
    loadTurtle: (text) => store.load(text, { format: 'text/turtle' }),
    dispose() {
      // Once only: freeing twice would hand Oxigraph a dangling pointer.
      if (disposed) return
      disposed = true
      ;(store as unknown as { free?: () => void }).free?.()
    },
  }
}

