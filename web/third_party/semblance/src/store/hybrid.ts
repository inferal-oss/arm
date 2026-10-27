import type { Quad, Term } from '@rdfjs/types'
import type * as Ox from 'oxigraph'
import { n3Store } from './n3.ts'
import type { GraphStore } from './types.ts'

/**
 * Oxigraph for SPARQL, an N3.js mirror for pattern matching. Every write goes
 * to both; `match` (the hot path for function rules) never crosses into WASM.
 * CONSTRUCT results are copied into mirror terms.
 */
export function hybridStore(ox: typeof Ox): GraphStore {
  let sparql = new ox.Store()
  const mirror = n3Store()
  const m = mirror.factory
  const copy = (t: Ox.Term) =>
    t.termType === 'NamedNode'
      ? m.namedNode(t.value)
      : t.termType === 'Literal'
        ? m.literal(t.value, t.language || m.namedNode(t.datatype.value))
        : t.termType === 'DefaultGraph'
          ? m.defaultGraph()
          : (t as never)
  const mirrorQuad = (q: Ox.Quad) =>
    m.quad(copy(q.subject as Ox.Term), copy(q.predicate as Ox.Term), copy(q.object as Ox.Term), copy(q.graph as Ox.Term))

  // Oxigraph keeps deleted quads as dead versions that later lookups walk (see
  // oxigraph.ts). Here the live quads are known in JS (the mirror), so the
  // SPARQL side is rebuilt a slice at a time in idle time, writes going to both
  // copies meanwhile, and swapped when complete: no pause, bounded dead weight.
  let deletes = 0
  let live = 0 // the size at the last rebuild: a good enough estimate of the live set
  let rebuild: { fresh: Ox.Store; todo: Quad[]; next: number } | undefined
  const due = () => deletes > Math.max(REBUILD_MIN, live / 2)

  let disposed = false
  return {
    kind: 'hybrid',
    // Mirror terms are plain JS objects and Oxigraph accepts any RDF/JS term,
    // so the mirror's factory is the cheaper one to hand out.
    factory: m,
    sparql: true,
    add(q) {
      sparql.add(q as Ox.Quad)
      rebuild?.fresh.add(q as Ox.Quad)
      mirror.add(q)
    },
    delete(q) {
      sparql.delete(q as Ox.Quad)
      rebuild?.fresh.delete(q as Ox.Quad)
      mirror.delete(q)
      deletes++
    },
    has: (q) => mirror.has(q),
    match: (s, p, o, g) => mirror.match(s, p, o, g),
    select: (query, graphs) => sparql.query(query, { default_graph: graphs as Ox.NamedNode[] }) as unknown as Map<string, Term>[],
    construct: (query, graphs) => (sparql.query(query, { default_graph: graphs as Ox.NamedNode[] }) as unknown as Ox.Quad[]).map(mirrorQuad),
    loadTurtle(text) {
      sparql.load(text, { format: 'text/turtle' })
      mirror.loadTurtle(text)
    },
    dispose() {
      if (disposed) return
      disposed = true
      const free = (st: Ox.Store | undefined) => (st as unknown as { free?: () => void } | undefined)?.free?.()
      free(sparql)
      free(rebuild?.fresh)
      rebuild = undefined
    },
    maintain(budgetMs) {
      const until = performance.now() + budgetMs
      if (!rebuild) {
        if (!due()) return false
        rebuild = { fresh: new ox.Store(), todo: mirror.match(null, null, null, null), next: 0 }
        deletes = 0
      }
      const r = rebuild
      while (r.next < r.todo.length && performance.now() < until) {
        const end = Math.min(r.todo.length, r.next + 256)
        // A quad deleted since the snapshot was taken is gone from the mirror: skip it.
        for (; r.next < end; r.next++) if (mirror.has(r.todo[r.next]!)) r.fresh.add(r.todo[r.next] as Ox.Quad)
      }
      if (r.next < r.todo.length) return true
      ;(sparql as unknown as { free?: () => void }).free?.()
      sparql = r.fresh
      live = r.todo.length
      rebuild = undefined
      return due()
    },
  }
}

const REBUILD_MIN = 5_000
