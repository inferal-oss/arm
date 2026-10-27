// Exact dependencies of a partitioned view (docs/IVM.md §6.2, §6.4, §9.3): which partitions a
// changed triple can touch. Every triple pattern of the view's body - in OPTIONAL, UNION, MINUS,
// EXISTS / NOT EXISTS, subqueries, property paths - is a dependency. A changed triple is routed
// to the patterns whose constant positions it matches; for each, a delta query binds the pattern
// to the triple and probes the store's own indexes through the patterns that occur with it in a
// solution (its group and the groups around it) until it reaches the partition key: those are
// the partitions to evaluate again. Probes read the store with this tick's removed triples put
// back (the union of the old and the new state), so a solution that existed before the change,
// or exists after it, is found either way. A pattern that cannot reach the key (a setting that
// gates every row; anything under LIMIT) is global: a change to it re-evaluates the view once.
// An aggregate subquery maintained as a part of its own (a derived input) contributes no patterns:
// a change of its rows binds the variables it projects, and probes from there.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import { termKey } from '../rdf.ts'
import type { Factory } from '../store/types.ts'
import { certainVars, type Expr, type Group, type Node, type Path, projectedVars, type Select } from './sparql.ts'

const RDF_TYPE = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#type'

/** A position of a pattern: a variable, or a constant term. */
export type Pos = { v: string } | { t: Term }

interface Scope {
  parent?: Scope
  /** Changes here can affect rows they do not connect to (under LIMIT / OFFSET). */
  global: boolean
  pats: Pat[]
  aliases: Alias[]
  /**
   * The variables its group certainly binds (OPTIONAL, MINUS, a UNION branch, a subquery): only
   * through these does a walk leave it - one it may leave unbound is compatible with any value
   * outside. Undefined: any (the body; EXISTS, which sees its outer row bound).
   */
  certain?: Set<string>
}

export interface Pat {
  id: number
  s: Pos
  /** A plain predicate (or a variable), or a path. */
  p: Pos | { path: Path }
  o: Pos
  /** Inside GRAPH: the graph (a variable or a named graph); else the union default graph. */
  g?: Pos
  /**
   * p* or p?, whose zero-length solutions are a node of the graph with itself: any triple bringing
   * a node in is its input - unless another pattern of the solution already holds that node.
   */
  zero?: boolean
  scope: Scope
}

/** BIND(?from AS ?to), BIND(COALESCE(?a, …) AS ?to), BIND(IF(…, ?a, ?b) AS ?to): ?to is one of ?from. */
interface Alias {
  to: string
  from: string[]
  /** Whether the first source, when bound, is the value (COALESCE, a plain alias). */
  first: boolean
  scope: Scope
}

/**
 * A triple lookup over the view's dataset (and the tick's removed triples): the union default
 * graph, or with `g` the view's named graphs (`g` one, null any), quads with their graph.
 */
export type Lookup = (s: Term | null, p: Term | null, o: Term | null, g?: Term | null) => Quad[]

export class Dependencies {
  readonly patterns: Pat[] = []
  private byPredicate = new Map<string, Pat[]>()
  private anyPredicate: Pat[] = []
  private fresh = 0
  private readonly nn = (iri: string) => this.f.namedNode(iri)
  /** What could not be analysed (the view is then recomputed). */
  problem?: string
  /** While collecting: the graph of the GRAPH pattern being read. */
  private graph?: Pos
  /** Derived subqueries, in the order given: the scope they sit in, and their variables as named there. */
  private derivedAt: { scope: Scope; vars: Map<string, string> }[] = []

  constructor(
    private readonly f: Factory,
    where: Group,
    readonly keys: string[],
    private readonly derived: readonly Select[] = [],
    /**
     * Prune also by patterns only partly bound (some triple must agree with what is bound), and when
     * the change binds the key itself: fewer partitions for a rule (its focus), at a lookup per check.
     */
    private readonly semiJoin = false,
  ) {
    const root: Scope = { global: false, pats: [], aliases: [] }
    try {
      this.group(where, root, (v) => v)
    } catch (e) {
      this.problem = (e as Error).message
    }
    for (const p of this.patterns) {
      if (!('path' in p.p) || p.p.path.k !== 'mod' || p.p.path.mod === '+') continue
      const held = (x: Pos) => {
        if ('t' in x) return false
        for (let sc: Scope | undefined = p.scope; sc; sc = sc.parent) if (sc.pats.some((y) => y !== p && !('path' in y.p) && [y.s, y.p as Pos, y.o].some((z) => 'v' in z && z.v === x.v))) return true
        return false
      }
      p.zero = !held(p.s) && !held(p.o)
    }
    for (const p of this.patterns) {
      const iris = 'path' in p.p ? pathIris(p.p.path) : 't' in p.p ? [p.p.t.value] : undefined
      if (!iris || zeroLength(p)) this.anyPredicate.push(p)
      else for (const iri of iris) this.byPredicate.set(iri, [...(this.byPredicate.get(iri) ?? []), p])
    }
  }

  /** The predicates a changed triple must have to reach a pattern, and whether some pattern takes any predicate. */
  predicates(): { iris: string[]; any: boolean } {
    return { iris: [...this.byPredicate.keys()], any: this.anyPredicate.length > 0 }
  }

  /** Root patterns over the key alone (and constants): a new key must match them to have rows. */
  anchors(): Pat[] {
    return this.patterns.filter((p) => !p.scope.parent && !p.g && !('path' in p.p) && [p.s, p.p as Pos, p.o].every((x) => 't' in x || this.keys.includes(x.v)))
  }

  /** Patterns a changed triple matches (by their constant positions). */
  route(q: Quad): Pat[] {
    const out: Pat[] = []
    for (const p of [...(this.byPredicate.get(q.predicate.value) ?? []), ...this.anyPredicate]) {
      if ('t' in p.s && !same(p.s.t, q.subject)) {
        if (!('path' in p.p)) continue
      }
      if ('t' in p.o && !same(p.o.t, q.object)) {
        if (!('path' in p.p)) continue
      }
      if ('path' in p.p && p.p.path.k === 'nps' && npsSides(p.p.path).every((side) => side.exclude.has(q.predicate.value))) continue
      if (p.g && (q.graph.termType !== 'NamedNode' || ('t' in p.g && !p.g.t.equals(q.graph)))) continue
      out.push(p)
    }
    return out
  }

  /**
   * The partition keys a changed triple (matching `p`) may touch, or 'global' when the change may
   * touch any partition. A key the probe reaches only in part (a setting of every row of one
   * configuration, in a cross product) has `undefined` where any value goes: a slice of the
   * partitions. Probes `lookup` (old and new state together).
   */
  probe(p: Pat, q: Quad, lookup: Lookup): (Term | undefined)[][] | 'global' {
    if (p.scope.global || this.inGlobal(p.scope)) return 'global'
    const starts = this.bind(p, q, lookup)
    if (starts === 'global') return 'global'
    return this.keysOf(starts.map((start) => this.walk(p.scope, p, start, lookup)))
  }

  /**
   * The partition keys a row of derived subquery `i` (appearing or going) may touch: its
   * variables bound as the row binds them, joined toward the key through the patterns around it.
   */
  probeDerived(i: number, row: ReadonlyMap<string, Term>, lookup: Lookup): (Term | undefined)[][] | 'global' {
    const d = this.derivedAt[i]!
    if (this.inGlobal(d.scope)) return 'global'
    const start = new Map<string, Term>()
    for (const [inner, outer] of d.vars) {
      const t = row.get(inner)
      if (t) start.set(outer, t)
    }
    return this.keysOf([this.walk(d.scope, undefined, start, lookup)])
  }

  private keysOf(walks: (Map<string, Term>[] | 'global')[]): (Term | undefined)[][] | 'global' {
    const out = new Map<string, (Term | undefined)[]>()
    for (const rows of walks) {
      if (rows === 'global') return 'global'
      for (const r of rows) {
        const key = this.keys.map((k) => r.get(k))
        out.set(key.map((t) => (t ? termKey(t) : '')).join(' '), key)
      }
    }
    return [...out.values()]
  }

  private inGlobal(s: Scope | undefined): boolean {
    for (; s; s = s.parent) if (s.global) return true
    return false
  }

  /** Initial bindings of a pattern to a changed triple (several for closures). */
  private bind(p: Pat, q: Quad, lookup: Lookup): Map<string, Term>[] | 'global' {
    const row = new Map<string, Term>()
    const put = (pos: Pos, t: Term) => {
      if ('t' in pos) return same(pos.t, t)
      const had = row.get(pos.v)
      if (had) return had.equals(t)
      row.set(pos.v, t)
      return true
    }
    if (p.g && !put(p.g, q.graph)) return []
    if (!('path' in p.p)) return put(p.s, q.subject) && put(p.p, q.predicate) && put(p.o, q.object) ? [row] : []
    // p* and p? also hold of every node of the graph with itself: a triple's nodes (a constant end, once it is one).
    const zero: Map<string, Term>[] = []
    if (zeroLength(p))
      for (const n of [q.subject, q.object]) {
        const r = new Map(row)
        if ([p.s, p.o].every((x) => ('t' in x ? same(x.t, n) : (r.set(x.v, n), true)))) zero.push(r)
      }
    const steps = this.steps(p, q, lookup, row)
    return steps === 'global' ? steps : [...zero, ...steps]
  }

  /** Bindings of a path pattern to a changed triple that is a step of it. */
  private steps(p: Pat, q: Quad, lookup: Lookup, row: Map<string, Term>): Map<string, Term>[] | 'global' {
    const put = (pos: Pos, t: Term) => {
      if ('t' in pos) return same(pos.t, t)
      const had = row.get(pos.v)
      if (had) return had.equals(t)
      row.set(pos.v, t)
      return true
    }
    const path = (p.p as { path: Path }).path
    // A path inside GRAPH walks that graph.
    if (p.g) lookup = inGraph(lookup, q.graph)
    // A link, an alternative of links, a negated set: the triple is one step.
    const direct = linkSteps(path)
    if (direct) {
      const out: Map<string, Term>[] = []
      for (const { iri, inv, exclude } of direct) {
        if (iri !== undefined && iri !== q.predicate.value) continue
        if (exclude?.has(q.predicate.value)) continue
        row.clear()
        if (p.g) put(p.g, q.graph)
        if (inv ? put(p.s, q.object) && put(p.o, q.subject) : put(p.s, q.subject) && put(p.o, q.object)) out.push(new Map(row))
      }
      return out
    }
    // A closure (p*, p+, p?) of a link or an alternative of links: the triple is one step of a
    // walk; the walk's start reaches its subject backwards, its end is reached from its object.
    if (path.k === 'mod') {
      // p*, p+, p? of a step or of a sequence of steps: the triple is one step, somewhere in it.
      const segments = path.p.k === 'seq' ? path.p.ps : [path.p]
      const out: Map<string, Term>[] = []
      const many = (nodes: Term[], back: boolean) => {
        if (path.mod === '?') return nodes
        const all = new Map<string, Term>()
        for (const n of nodes) for (const m of this.closure(path.p, n, back, lookup)) all.set(termKey(m), m)
        return [...all.values()]
      }
      const through = (nodes: Term[], segs: Path[], back: boolean) => {
        for (const seg of back ? [...segs].reverse() : segs) {
          const next = new Map<string, Term>()
          for (const n of nodes) for (const m of walkPath(this.nn, seg, n, back, lookup)) next.set(termKey(m), m)
          nodes = [...next.values()]
        }
        return nodes
      }
      for (let i = 0; i < segments.length; i++) {
        const steps = linkSteps(segments[i]!)
        if (!steps) return 'global'
        // The triple is a step from its subject to its object, or (an inverse step) back.
        for (const inv of new Set(steps.filter((x) => (x.iri === q.predicate.value || x.iri === undefined) && !x.exclude?.has(q.predicate.value)).map((x) => x.inv))) {
          const [from, to] = inv ? [q.object, q.subject] : [q.subject, q.object]
          const starts = many(through([from], segments.slice(0, i), true), true).filter((n) => !('t' in p.s) || same(p.s.t, n))
          const ends = many(through([to], segments.slice(i + 1), false), false).filter((n) => !('t' in p.o) || same(p.o.t, n))
          if (!starts.length || !ends.length) continue
          // A solution through this step has its start among the starts and its end among the ends:
          // both bound (while there are few pairs), else each end on its own.
          if ('v' in p.s && 'v' in p.o && starts.length * ends.length <= 2000) {
            for (const a of starts) for (const b of ends) out.push(new Map([[p.s.v, a], [p.o.v, b]]))
            continue
          }
          if ('v' in p.s) for (const n of starts) out.push(new Map([[p.s.v, n]]))
          if ('v' in p.o) for (const n of ends) out.push(new Map([[p.o.v, n]]))
          if ('t' in p.s && 't' in p.o) out.push(new Map())
        }
      }
      if (p.g && 'v' in p.g) for (const r of out) r.set(p.g.v, q.graph)
      return out
    }
    return 'global'
  }

  /** Nodes reached from `from` (or reaching it, `back`) by zero or more steps of a path. */
  private closure(path: Path, from: Term, back: boolean, lookup: Lookup): Term[] {
    const seen = new Map<string, Term>([[termKey(from), from]])
    const todo = [from]
    while (todo.length) {
      const n = todo.pop()!
      for (const m of walkPath(this.nn, path, n, back, lookup)) {
        const k = termKey(m)
        if (seen.has(k)) continue
        if (seen.size > 10_000) return [...seen.values()]
        seen.set(k, m)
        todo.push(m)
      }
    }
    return [...seen.values()]
  }

  /** From bindings in a scope (a pattern bound to a triple, or a derived row), join through the patterns around them until the keys are bound. */
  private walk(scope: Scope, p: Pat | undefined, start: Map<string, Term>, lookup: Lookup): Map<string, Term>[] | 'global' {
    const bound = new Set(start.keys())
    // The patterns (and aliases) that occur with p in a solution: its scope and the scopes around it.
    const { around, aliases, byVar } = this.neighbours(scope, p)
    // Breadth-first over variables, from the bound ones to each key.
    type Edge = { pat?: Pat; alias?: Alias; dir?: 'out' | 'in'; from: string }
    const via = new Map<string, Edge>()
    const queue = [...bound]
    const reached = new Set(bound)
    // Where each variable is bound: a walk leaves a scope only through what it certainly binds.
    const level = new Map<string, Scope>([...bound].map((v) => [v, scope]))
    const leaves = (v: string, to: Scope) => {
      let s: Scope | undefined = level.get(v)
      const inside = (x: Scope | undefined) => {
        for (; x; x = x.parent) if (x === to) return true
        return false
      }
      if (!inside(s)) return true
      for (; s && s !== to; s = s.parent) if (s.certain && !s.certain.has(v)) return false
      return true
    }
    const reach = (w: string, edge: Edge, at: Scope) => {
      if (reached.has(w)) return
      reached.add(w), via.set(w, edge), queue.push(w), level.set(w, at)
    }
    const varsOf = patVars
    while (queue.length && !this.keys.every((k) => reached.has(k))) {
      const v = queue.shift()!
      for (const x of byVar.get(v) ?? []) {
        const vs = varsOf(x)
        if (!leaves(v, x.scope)) continue
        // Path connectors need a bound end to walk from.
        for (const w of vs) reach(w, { pat: x, from: v }, x.scope)
      }
      for (const a of aliases) {
        if (!leaves(v, a.scope)) continue
        if (a.to === v) for (const w of a.from) reach(w, { alias: a, dir: 'out', from: v }, a.scope)
        if (a.first && a.from[0] === v) reach(a.to, { alias: a, dir: 'in', from: v }, a.scope)
      }
    }
    // No part of the key reached: any partition - unless the change joins with nothing around it
    // (then it is in no solution). Part of it: the slice it binds.
    const unreachable = this.keys.length > 0 && !this.keys.some((k) => reached.has(k))
    // The edges on the way to each key, in the order they were found (unreachable: all of them).
    const needed: Edge[] = unreachable ? [...via.values()] : []
    const seen = new Set<Edge>()
    for (const k of unreachable ? [] : this.keys) {
      if (!reached.has(k)) continue
      const chain: Edge[] = []
      for (let w = k; via.has(w); w = via.get(w)!.from) chain.push(via.get(w)!)
      for (const e of chain.reverse()) if (!seen.has(e)) seen.add(e), needed.push(e)
    }
    // Patterns that hold in every solution p takes part in (its scope and those around it; not
    // paths) prune the rows once they bind all of a pattern's variables: a semi-join, so a probe
    // reaches the partitions that hold the change, not every one its first edge leads to.
    const checks = around
    const holds = (r: Map<string, Term>) =>
      checks.every((x) => {
        // (Only with what may leave its scope for x's: a variable an inner group may leave unbound is no constraint.)
        if (!varsOf(x).every((v) => !r.has(v) || leaves(v, x.scope))) return true
        const val = (pos: Pos) => ('t' in pos ? pos.t : r.get(pos.v))
        const s = val(x.s)
        const pp = val(x.p as Pos)
        const o = val(x.o)
        // With a part unbound, it still needs some triple that agrees with what is bound (a
        // semi-join): unless nothing of it is bound by the row, which would say nothing about the row.
        // No triple has a literal subject (and the store would refuse to look).
        if (s && s.termType === 'Literal') return false
        if (!s || !pp || !o) {
          if (!this.semiJoin) return true
          // (Only through an index of a bound node: a predicate alone would read all of it.)
          if (!varsOf(x).some((v) => r.has(v)) || (!s && !o)) return true
          if (pp && pp.termType !== 'NamedNode') return true
        }
        if (!x.g) return lookup(s ?? null, pp ?? null, o ?? null).length > 0
        return lookup(s ?? null, pp ?? null, o ?? null, val(x.g) ?? null).length > 0
      })
    // Bound already: the key is the change's own (with semiJoin, if the patterns around it hold).
    if (this.keys.every((k) => bound.has(k))) return this.semiJoin ? [start].filter(holds) : [start]
    let rows = [start].filter(holds)
    const done = new Set<Pat | Alias>()
    for (const e of needed) {
      const x = (e.pat ?? e.alias)!
      if (e.pat && done.has(x)) continue
      done.add(x)
      const next: Map<string, Term>[] = []
      for (const r of rows) {
        if (e.pat) next.push(...this.extend(e.pat, r, lookup))
        else {
          const a = e.alias!
          if (e.dir === 'out') {
            const t = r.get(a.to)
            if (!t) continue
            for (const w of a.from) {
              const had = r.get(w)
              if (had && !had.equals(t)) continue
              next.push(new Map(r).set(w, t))
            }
          } else {
            const t = r.get(a.from[0]!)
            if (t && !r.has(a.to)) next.push(new Map(r).set(a.to, t))
            else next.push(r)
          }
        }
        if (next.length > 50_000) return 'global'
      }
      rows = dedupe(next).filter(holds)
      if (!rows.length) return []
    }
    if (unreachable) return rows.length ? 'global' : []
    return rows.filter((r) => this.keys.every((k) => r.has(k) || !reached.has(k)))
  }

  private neighbourhoods = new Map<Pat | Scope, { around: Pat[]; aliases: Alias[]; byVar: Map<string, Pat[]> }>()

  /**
   * What a walk from pattern `p` (or from a derived row in `scope`) may go through: the patterns of
   * its scope and the scopes around it, which hold in every solution p takes part in (so they also
   * prune: `around`, without paths). Not the groups beside them: an OPTIONAL or a UNION branch need
   * not match where a solution does, so a walk through one could miss it. By variable; cached.
   */
  private neighbours(scope: Scope, p: Pat | undefined) {
    const key = p ?? scope
    let n = this.neighbourhoods.get(key)
    if (n) return n
    const pats: Pat[] = []
    const aliases: Alias[] = []
    for (let s: Scope | undefined = scope; s; s = s.parent) pats.push(...s.pats.filter((x) => x !== p)), aliases.push(...s.aliases)
    const inner = new Set(pats)
    const byVar = new Map<string, Pat[]>()
    for (const x of pats) for (const v of new Set(patVars(x))) byVar.set(v, [...(byVar.get(v) ?? []), x])
    n = { around: [...inner].filter((x) => !('path' in x.p)), aliases, byVar }
    this.neighbourhoods.set(key, n)
    return n
  }

  /** Rows extended by one pattern, looked up with what the row binds. */
  private extend(x: Pat, r: Map<string, Term>, lookup: Lookup): Map<string, Term>[] {
    const val = (pos: Pos) => ('t' in pos ? pos.t : r.get(pos.v))
    const s = val(x.s)
    const o = val(x.o)
    const out: Map<string, Term>[] = []
    const set = (row: Map<string, Term>, pos: Pos, t: Term) => {
      if ('t' in pos) return same(pos.t, t)
      const had = row.get(pos.v)
      if (had) return had.equals(t)
      row.set(pos.v, t)
      return true
    }
    if (x.g) {
      // Inside GRAPH: a graph that is known, or each of the view's graphs the lookup finds.
      const g = val(x.g)
      if (!g && 'path' in x.p) return []
      if (g) lookup = inGraph(lookup, g)
    }
    if ('path' in x.p) {
      // Walk the path from a bound end.
      const path = x.p.path
      if (s) for (const n of walkPath(this.nn, path, s, false, lookup)) {
        const row = new Map(r)
        if (set(row, x.o, n)) out.push(row)
      }
      else if (o) for (const n of walkPath(this.nn, path, o, true, lookup)) {
        const row = new Map(r)
        if (set(row, x.s, n)) out.push(row)
      }
      return out
    }
    const p = val(x.p)
    if (!s && !o && !p) return []
    for (const q of x.g ? lookup(s ?? null, (p as NamedNode) ?? null, o ?? null, val(x.g) ?? null) : lookup(s ?? null, (p as NamedNode) ?? null, o ?? null)) {
      const row = new Map(r)
      if ((!x.g || set(row, x.g, q.graph)) && set(row, x.s, q.subject) && set(row, x.p as Pos, q.predicate) && set(row, x.o, q.object)) out.push(row)
    }
    return out
  }

  // --- collecting the patterns -------------------------------------------------------------------

  private pos(n: Node, rename: (v: string) => string): Pos {
    switch (n.k) {
      case 'var':
        return { v: rename(n.v) }
      case 'bnode':
        return { v: rename('_:' + n.v) }
      case 'iri':
        return { t: this.f.namedNode(n.v) }
      case 'lit':
        return { t: n.lang ? this.f.literal(n.value, n.lang) : this.f.literal(n.value, n.dt ? this.f.namedNode(n.dt) : undefined) }
    }
  }

  private add(scope: Scope, s: Pos, p: Pos | { path: Path }, o: Pos) {
    if (this.graph && 'v' in this.graph && 'path' in p) throw new Error('a property path inside GRAPH ?var')
    const pat: Pat = { id: this.patterns.length, s, p, o, g: this.graph, scope }
    this.patterns.push(pat)
    scope.pats.push(pat)
  }

  private triple(scope: Scope, s: Pos, p: Node | Path, o: Pos, rename: (v: string) => string): void {
    if (p.k === 'var') return this.add(scope, s, { v: rename(p.v) }, o)
    if (p.k === 'iri' || p.k === 'lit' || p.k === 'bnode') return this.add(scope, s, this.pos(p, rename), o)
    // Paths: a sequence is a chain through fresh variables, an inverse swaps its ends.
    if (p.k === 'link') return this.add(scope, s, { t: this.f.namedNode(p.iri) }, o)
    if (p.k === 'inv') return this.triple(scope, o, p.p, s, rename)
    if (p.k === 'seq') {
      let from = s
      p.ps.forEach((step, i) => {
        const to: Pos = i === p.ps.length - 1 ? o : { v: `__p${this.fresh++}` }
        this.triple(scope, from, step, to, rename)
        from = to
      })
      return
    }
    this.add(scope, s, { path: p }, o)
  }

  /**
   * `left`: inside a LATERAL, the variables its left side certainly binds (as named here): a
   * subquery with LIMIT that projects one of them is evaluated per binding of it (correlated), so a
   * change reaches the bindings it joins to, not every partition.
   */
  private group(g: Group, scope: Scope, rename: (v: string) => string, left: ReadonlySet<string> = new Set()) {
    g.items.forEach((it, i) => {
      switch (it.k) {
        case 'triples':
          for (const t of it.triples) this.triple(scope, this.pos(t.s, rename), t.p, this.pos(t.o, rename), rename)
          break
        case 'lateral': {
          const before = [...certainVars({ items: g.items.slice(0, i), at: g.at, end: g.end })].map(rename)
          this.group(it.g, scope, rename, new Set([...left, ...before]))
          break
        }
        case 'group':
          this.group(it.g, scope, rename, left)
          break
        case 'graph': {
          const outer = this.graph
          this.graph = this.pos(it.name, rename)
          // A graph's solutions can need no triple of it (GRAPH ?g {}, a FILTER, an OPTIONAL): that the
          // graph has any quad is a dependency too.
          if (!it.g.items.some((x) => x.k === 'triples')) this.add(scope, { v: `__q${this.fresh++}` }, { v: `__q${this.fresh++}` }, { v: `__q${this.fresh++}` })
          this.group(it.g, scope, rename, left)
          this.graph = outer
          break
        }
        case 'optional':
        case 'minus':
          this.group(it.g, this.child(scope, false, this.certainIn(it.g, rename)), rename, left)
          break
        case 'union':
          for (const b of it.gs) this.group(b, this.child(scope, false, this.certainIn(b, rename)), rename, left)
          break
        case 'service':
          throw new Error('SERVICE')
        case 'filter':
          this.expr(it.e, scope, rename)
          break
        case 'bind':
          this.expr(it.e, scope, rename)
          this.alias(it.e, rename(it.as), scope, rename)
          break
        case 'values':
          break
        case 'select':
          this.select(it.q, scope, rename, left)
      }
    })
  }

  private child(parent: Scope, global = false, certain?: Set<string>): Scope {
    return { parent, global, pats: [], aliases: [], certain }
  }

  private certainIn(g: Group, rename: (v: string) => string): Set<string> {
    return new Set([...certainVars(g)].map(rename))
  }

  private expr(e: Expr, scope: Scope, rename: (v: string) => string) {
    for (const x of e.exists) this.group(x.g, this.child(scope), rename)
    for (const a of e.aggregates) if (a.arg !== '*') this.expr(a.arg, scope, rename)
  }

  private alias(e: Expr, to: string, scope: Scope, rename: (v: string) => string) {
    const t = e.text.trim()
    const v = /^[?$](\w+)$/.exec(t)
    if (v) return void scope.aliases.push({ to, from: [rename(v[1]!)], first: true, scope })
    const call = /^(COALESCE|IF)\s*\(([\s\S]*)\)$/i.exec(t)
    if (!call) return
    const args = splitArgs(call[2]!)
    const isIf = call[1]!.toUpperCase() === 'IF'
    const vars = (isIf ? args.slice(1) : args).map((a) => /^[?$](\w+)$/.exec(a.trim())?.[1]).filter((x): x is string => !!x).map(rename)
    if (vars.length) scope.aliases.push({ to, from: vars, first: !isIf && /^[?$]\w+$/.test(args[0]?.trim() ?? ''), scope })
  }

  private select(q: Select, scope: Scope, rename: (v: string) => string, left: ReadonlySet<string> = new Set()) {
    // A subquery's own variables are its own: only what it projects joins the outside.
    const projected = new Set(projectedVars(q))
    const id = this.fresh++
    const inner = (v: string) => (projected.has(v) ? rename(v) : `${v}#${id}`)
    const at = this.derived.indexOf(q)
    if (at >= 0) {
      // A derived input: its own part follows its patterns; here only its rows matter.
      this.derivedAt[at] = { scope, vars: new Map([...projected].map((v) => [v, rename(v)])) }
      return
    }
    const correlated = [...projected].some((v) => left.has(rename(v)))
    const certain = new Set([...certainVars(q.where), ...q.projection.filter((x) => x.e).map((x) => x.v)].filter((v) => projected.has(v)).map(inner))
    const s = this.child(scope, (q.limit !== undefined || q.offset !== undefined) && !correlated, certain)
    // Projected expressions alias their variables.
    for (const p of q.projection) if (p.e) this.alias(p.e, inner(p.v), s, inner)
    this.group(q.where, s, inner, left)
    for (const p of q.projection) if (p.e) this.expr(p.e, s, inner)
    for (const h of q.having) this.expr(h, s, inner)
    for (const e of q.groupBy) {
      this.expr(e, s, inner)
      if (e.as) this.alias(e, inner(e.as), s, inner)
    }
  }
}

const NUMERIC = /#(integer|decimal|double|float|int|long|short|byte|nonNegativeInteger|positiveInteger|nonPositiveInteger|negativeInteger|unsignedInt|unsignedLong|unsignedShort|unsignedByte)$/
/**
 * A query's constant and a stored term: equal, or the same number (a store may keep "01" and "+1"
 * as 1, and a query may write either): matching more is safe, a dependency missed is not.
 */
const varCache = new WeakMap<Pat, string[]>()
/** A pattern's variables (its subject, plain predicate, object and graph). */
function patVars(x: Pat): string[] {
  let vs = varCache.get(x)
  if (!vs) varCache.set(x, (vs = [x.s, 'path' in x.p ? undefined : x.p, x.o, x.g].filter((y): y is { v: string } => !!y && 'v' in y).map((y) => y.v)))
  return vs
}

function same(constant: Term, t: Term): boolean {
  if (constant.equals(t)) return true
  return constant.termType === 'Literal' && t.termType === 'Literal' && NUMERIC.test(constant.datatype.value) && constant.datatype.equals(t.datatype) && Number(constant.value) === Number(t.value)
}

/** A lookup confined to one named graph (paths inside GRAPH). */
const inGraph = (lookup: Lookup, g: Term): Lookup => (s, p, o) => lookup(s, p, o, g)

function dedupe(rows: Map<string, Term>[]): Map<string, Term>[] {
  const seen = new Map<string, Map<string, Term>>()
  for (const r of rows) {
    const k = [...r.entries()].sort(([a], [b]) => (a < b ? -1 : 1)).map(([a, t]) => a + '=' + termKey(t)).join(' ')
    if (!seen.has(k)) seen.set(k, r)
  }
  return [...seen.values()]
}

/** Split a call's arguments at top-level commas. */
export function splitArgs(s: string): string[] {
  const out: string[] = []
  let depth = 0
  let cur = ''
  let quote = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]!
    if (quote) {
      cur += c
      if (c === '\\') cur += s[++i] ?? ''
      else if (c === quote) quote = ''
      continue
    }
    if (c === '"' || c === "'") quote = c
    else if (c === '(' || c === '{') depth++
    else if (c === ')' || c === '}') depth--
    else if (c === ',' && depth === 0) {
      out.push(cur)
      cur = ''
      continue
    }
    cur += c
  }
  if (cur.trim()) out.push(cur)
  return out
}

/** The predicates a path may step through; undefined: any (a negated set). */
function pathIris(p: Path): string[] | undefined {
  switch (p.k) {
    case 'link':
      return [p.iri]
    case 'inv':
    case 'mod':
      return pathIris(p.p)
    case 'seq':
    case 'alt': {
      const all = p.ps.map(pathIris)
      return all.some((x) => !x) ? undefined : all.flat() as string[]
    }
    case 'nps':
      return undefined
  }
}

/** A p* or p?: true of every node of the graph with itself, so any triple is its input. */
const zeroLength = (p: Pat) => !!p.zero

/** A negated property set's directions: forward (not these), and inverse (not those), as written. */
function npsSides(p: Extract<Path, { k: 'nps' }>): { inv: boolean; exclude: Set<string> }[] {
  const fwd = p.iris.filter((x) => !x.inv).map((x) => x.iri)
  const back = p.iris.filter((x) => x.inv).map((x) => x.iri)
  const out: { inv: boolean; exclude: Set<string> }[] = []
  if (fwd.length || !back.length) out.push({ inv: false, exclude: new Set(fwd) })
  if (back.length) out.push({ inv: true, exclude: new Set(back) })
  return out
}

/** A path that is one step (a link, an inverse, an alternative of those, a negated set): its steps. */
function linkSteps(p: Path, inv = false): { iri?: string; inv: boolean; exclude?: Set<string> }[] | undefined {
  switch (p.k) {
    case 'link':
      return [{ iri: p.iri, inv }]
    case 'inv':
      return linkSteps(p.p, !inv)
    case 'alt': {
      const all = p.ps.map((x) => linkSteps(x, inv))
      return all.some((x) => !x) ? undefined : all.flat() as { iri?: string; inv: boolean; exclude?: Set<string> }[]
    }
    case 'nps':
      return npsSides(p).map((side) => ({ iri: undefined, inv: side.inv !== inv, exclude: side.exclude }))
    default:
      return undefined
  }
}

/** One step of a path from a node (forwards, or backwards with `back`). */


/** The nodes a path leads to from `n` (or that lead to `n`, `back`). */
export function walkPath(nn: (iri: string) => NamedNode, p: Path, n: Term, back: boolean, lookup: Lookup): Term[] {
  const out = new Map<string, Term>()
  const put = (t: Term) => out.set(termKey(t), t)
  switch (p.k) {
    case 'link': {
      const pred = nn(p.iri)
      for (const q of back ? lookup(null, pred, n) : lookup(n, pred, null)) put(back ? q.subject : q.object)
      break
    }
    case 'inv':
      for (const t of walkPath(nn, p.p, n, !back, lookup)) put(t)
      break
    case 'alt':
      for (const x of p.ps) for (const t of walkPath(nn, x, n, back, lookup)) put(t)
      break
    case 'seq': {
      let cur = [n]
      for (const x of back ? [...p.ps].reverse() : p.ps) {
        const next = new Map<string, Term>()
        for (const c of cur) for (const t of walkPath(nn, x, c, back, lookup)) next.set(termKey(t), t)
        cur = [...next.values()]
      }
      for (const t of cur) put(t)
      break
    }
    case 'mod': {
      if (p.mod !== '+') put(n)
      const seen = new Set<string>([termKey(n)])
      let frontier = [n]
      const once = p.mod === '?'
      while (frontier.length) {
        const next: Term[] = []
        for (const c of frontier) for (const t of walkPath(nn, p.p, c, back, lookup)) {
          put(t)
          const k = termKey(t)
          if (!seen.has(k) && !once) seen.add(k), next.push(t)
        }
        if (seen.size > 10_000) break
        frontier = next
      }
      break
    }
    case 'nps':
      for (const side of npsSides(p)) {
        const reverse = back !== side.inv
        for (const q of reverse ? lookup(null, null, n) : lookup(n, null, null)) if (!side.exclude.has(q.predicate.value)) put(reverse ? q.subject : q.object)
      }
      break
  }
  return [...out.values()]
}

export { RDF_TYPE }
