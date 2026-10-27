// Maintained views (docs/IVM.md): queries whose results are kept up to date from the changes
// to their inputs, one tick per transaction of the Runtime. A view's partitions (LDES members,
// rows of a collection, cells...) are evaluated on the store and cached; a changed triple
// reaches the partitions it can affect through the query's own patterns (deps.ts); above them,
// DISTINCT, GROUP BY and top-k are incremental (plan.ts). A view the compiler cannot maintain
// is recomputed from scratch when its inputs change, and says so (iv:fallback).
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import { md5 } from '../components/data.ts'
import { createContext, type RuleContext } from '../engine/context.ts'
import { NS, PROV, RDF_TYPE, SEM } from '../ns.ts'
import { prefixesFor } from '../prefixes.ts'
import { type Delta, mint, owningFocus, parseDuration, quadKey, termKey } from '../rdf.ts'
import type { GraphStore } from '../store/types.ts'
import { accSize, accumulate, compareTerms, type Group, newGroup, result } from './aggregate.ts'
import { compiler, keyString, prefixMap, sparqlTerm, TREE_MEMBER } from './compile.ts'
import { Dependencies, type Lookup } from './deps.ts'
import { parseQuery } from './sparql.ts'
import { OrderedMultiset } from './ordered.ts'
import type { FnView, MaintainedPlan, Part, Post, ViewCompiler, ViewPlan, ViewSource, WindowSource } from './plan.ts'
import { WindowState } from './window.ts'
import type { Node, Triple } from './sparql.ts'
import { Terms } from './terms.ts'
import { ZSet } from './zset.ts'

export const IV = 'https://semblance.dev/ns/view#'
/** The W3C result-set vocabulary: how a materialized SELECT view writes its rows. */
export const RS = 'http://www.w3.org/2001/sw/DataAccess/tests/result-set#'
/** Where materialized views keep their output, their plans and their figures. */
export const VIEW_GRAPH = 'urn:semblance:views'

export interface ViewClock {
  now(): number
  /** Run `fn` at clock time `at`; returns a cancel function. */
  schedule(at: number, fn: () => void): () => void
}

/** The wall clock. Timers only where there is a page to update (browsers); elsewhere expiry waits for the next tick. */
export const systemClock = (): ViewClock => ({
  now: () => Date.now(),
  schedule(at, fn) {
    if (typeof requestAnimationFrame === 'undefined') return () => {}
    const t = setTimeout(fn, Math.max(0, at - Date.now()))
    return () => clearTimeout(t)
  },
})

/** A clock tests move by hand. */
export class ManualClock implements ViewClock {
  private pending: { at: number; fn: () => void }[] = []
  constructor(public t = Date.parse('2026-01-01T00:00:00Z')) {}
  now = () => this.t
  schedule(at: number, fn: () => void) {
    const p = { at, fn }
    this.pending.push(p)
    return () => (this.pending = this.pending.filter((x) => x !== p))
  }
  advance(ms: number): void {
    this.t += ms
    for (const p of this.pending.filter((x) => x.at <= this.t)) {
      this.pending = this.pending.filter((x) => x !== p)
      p.fn()
    }
  }
}

export interface ViewFigures {
  ticks: number
  /** Rows processed: partition rows entering or leaving, groups updated, output rows. */
  rowsIn: number
  rowsOut: number
  ms: number
  /** The last tick that did work for this view. */
  lastRows: number
  lastMs: number
  /** Partitions evaluated (in all), and ticks a change reached every partition (a rebuild). */
  partitions: number
  rebuilds: number
}

/** Totals over every view (the status bar and the cost tests read these). */
export interface ViewTotals {
  ticks: number
  rowsIn: number
  /** Rows of operator state now held. */
  state: number
  ms: number
  /** Partitions evaluated, and rebuilds (a change reaching every partition). */
  partitions: number
  rebuilds: number
}

export interface ViewDelta {
  /** SELECT views: rows that appeared (+) or went (-). */
  rows?: ZSet<Map<string, Term>>
  /** CONSTRUCT views (and materialized SELECTs, their rs: rows): their own output's change. */
  quads?: ZSet<Quad>
}

/** A rule maintained as a view (ViewRuntime.hostRule). */
export interface RuleView {
  view: ViewHandle
  /** A focus's output now. */
  quads(focus: string): Quad[]
  /** Hear which foci's output changed (once per tick that changed any). */
  onChange(fn: (foci: Set<string>) => void): () => void
  dispose(): void
}

/** A view's change over several ticks, consolidated (ViewHandle.since): rows and triples both. */
export interface ViewChange {
  /** The runtime's tick it brings a reader up to. */
  tick: number
  rows: ZSet<Map<string, Term>>
  quads: ZSet<Quad>
}

export interface ViewHandle {
  readonly iri: string
  readonly strategy: 'incremental' | 'recompute'
  readonly fallback?: string
  readonly figures: ViewFigures
  /** The current result. */
  rows(): Map<string, Term>[]
  quads(): Quad[]
  /** SELECT views with ORDER BY: each row's position (1-based), by its key (as rows() keys it). */
  index(row: Map<string, Term>): number | undefined
  /** Net change per tick; `replay` first delivers the current result as one change. */
  subscribe(fn: (d: ViewDelta) => void, opts?: { replay?: boolean }): () => void
  /**
   * Everything that changed after tick `tick`, consolidated ("since you opened this page"), from the
   * changes the view keeps (keepHistory, or its iv:deltaLog's retention); undefined when they no
   * longer reach back that far - read the whole result instead.
   */
  since(tick: number): ViewChange | undefined
  /** Keep the changes of at least the last `ticks` ticks that changed the view. */
  keepHistory(ticks: number): void
  /** The whole result as one change (a reader starting from nothing). */
  snapshot(): ViewChange
  footprint(): Record<string, number>
}

export interface ViewOptions {
  /** The graphs a view reads besides the default graph (the runtime's input graphs). */
  graphs: string[]
  functions?: Record<string, FnView>
  compiler?: ViewCompiler
  clock?: ViewClock
}

const warn = (msg: string) => console.warn(`[semblance] ${msg}`)
const localName = (iri: string) => iri.replace(/^.*[#/]/, '')

/** A view's definition as read from the graph (a template of iv:targets is defined per instance). */
interface Definition {
  iri: string
  /** The view node its triples are on (the template, for instances). */
  node: string
  /** For an instance of a template: the instance ($this). */
  focus?: string
  /**
   * A view a host effect defines (ViewRuntime.host): its query, not read from the graph; with
   * `attribute`, its partitions are by that variable and each output row is attributed to its value
   * (a rule maintained as a view: the focus).
   */
  hosted?: { query: string; form: 'select' | 'construct'; attribute?: string }
}

/** A parameter, resolved: what it reads, so a change to that re-resolves it. */
interface Param {
  name: string
  value?: Term
  /** Whether a changed triple can change the parameter's value. */
  reads: (q: Quad) => boolean
}

export class ViewRuntime {
  readonly graph: NamedNode
  readonly terms = new Terms()
  readonly totals: ViewTotals = { ticks: 0, rowsIn: 0, state: 0, ms: 0, partitions: 0, rebuilds: 0 }
  /** Every tick the runtime has run, numbered (delta logs name ticks by it). */
  tickNo = 0
  /** Compare every view with a from-scratch evaluation after every tick, and refuse fallbacks (tests). */
  strict = false
  clock: ViewClock
  /** Called when a window's next expiry is due: the runtime runs a tick with no input. */
  onTimer?: () => void
  /**
   * Whether a component is shown (the runtime asks its projections). A view with
   * iv:whileShown is maintained only while that component - or, for a class, one of its
   * instances - is shown; otherwise it is suspended, and rebuilt when it is shown again.
   */
  shown?: (component: string) => boolean
  /** Times each view's step (the watchdog blames the slowest work of a turn). */
  watchdog?: { clock(): number; note(kind: 'view', iri: string, focus: string | undefined, count: number, ms: number): void }
  private views = new Map<string, MaintainedView>()
  private afterTicks: ((tick: number) => void)[] = []
  /** Views in the order they step: a view after the views it reads (iv:reads). */
  private order: MaintainedView[] = []
  /** Templates (iv:targets): template IRI -> its class. */
  private templates = new Map<string, string>()
  private refs = new Map<string, { q: Quad; n: number }>()
  private dataset: Set<string>
  readonly graphs: Term[]
  private started = false
  private prefer: 'sparql' | 'function' = 'sparql'
  private timer?: { at: number; cancel: () => void }
  private ctx: RuleContext
  private prefixText?: string

  /** The store's SPARQL prefixes (the framework's and those declared in the graph), cached until a declaration changes. */
  prefixes(): string {
    return (this.prefixText ??= prefixesFor(this.store))
  }

  constructor(
    readonly store: GraphStore,
    private readonly opts: ViewOptions,
  ) {
    const f = store.factory
    this.graph = f.namedNode(VIEW_GRAPH)
    this.graphs = [f.defaultGraph(), ...opts.graphs.map((g) => f.namedNode(g))]
    this.dataset = new Set(['', ...opts.graphs])
    this.clock = opts.clock ?? systemClock()
    this.ctx = createContext(store, [...this.graphs, this.graph] as never, '', [])
  }

  get isStarted(): boolean {
    return this.started
  }

  view(iri: string): ViewHandle | undefined {
    return this.views.get(iri)
  }

  all(): ViewHandle[] {
    return [...this.views.values()]
  }

  /** The instance of a template view (iv:targets) for one focus. */
  instanceOf(template: string, focus: string): string {
    return mint(focus, 'view-' + localName(template))
  }

  /** Compile every iv:View in the store and evaluate it; returns the change to the view graph (already applied). */
  start(prefer: 'sparql' | 'function' = 'sparql'): Delta {
    this.started = true
    this.prefer = prefer
    const out = new ZSet<Quad>()
    const f = this.store.factory
    for (const q of this.store.match(null, f.namedNode(RDF_TYPE), f.namedNode(IV + 'View'))) {
      if (q.subject.termType === 'NamedNode') for (const d of this.definitions(q.subject.value)) this.define(d, out)
    }
    for (const d of this.hosted.values()) this.define(d, out)
    this.sort()
    const now = this.clock.now()
    const applied: Delta = { add: [], remove: [] }
    const sinkNow = () => {
      const d = this.sink(out)
      applied.add.push(...d.add)
      applied.remove.push(...d.remove)
      out.clear()
    }
    sinkNow()
    for (const v of this.order) {
      if (this.demanded(v)) out.plus(v.initial(now))
      else v.suspended = true
      sinkNow()
    }
    this.check(now)
    this.reschedule()
    this.settled()
    return applied
  }

  /** The views a view node defines: itself, or (iv:targets) one per instance of the class. */
  private definitions(iri: string): Definition[] {
    const f = this.store.factory
    const cls = this.store.match(f.namedNode(iri), f.namedNode(IV + 'targets'), null)[0]?.object
    if (!cls) {
      this.templates.delete(iri)
      return [{ iri, node: iri }]
    }
    this.templates.set(iri, cls.value)
    return this.store
      .match(null, f.namedNode(RDF_TYPE), cls as NamedNode)
      .filter((q) => q.subject.termType === 'NamedNode')
      .map((q) => ({ iri: this.instanceOf(iri, q.subject.value), node: iri, focus: q.subject.value }))
  }

  /** One tick: the consolidated change of the input graphs. Returns the view graph's change (applied). */
  tick(delta: Delta): Delta {
    if (!this.started) return { add: [], remove: [] }
    this.tickNo++
    const t = performance.now()
    const now = this.clock.now()
    const inDataset = (q: Quad) => this.dataset.has(q.graph.value)
    const changes = { add: delta.add.filter(inDataset), remove: delta.remove.filter(inDataset) }
    if ([...changes.add, ...changes.remove].some((q) => q.predicate.value === NS.sh + 'prefix' || q.predicate.value === NS.sh + 'namespace')) this.prefixText = undefined
    const out = new ZSet<Quad>()
    // A view's own triples (its query, its window, its parameters) changed: compile it again.
    const redefine = new Set<string>()
    const nodes = new Set<string>()
    for (const q of [...changes.add, ...changes.remove]) {
      if (q.subject.termType !== 'NamedNode') continue
      const s = q.subject.value
      if (q.predicate.value === RDF_TYPE && q.object.value === IV + 'View') nodes.add(s)
      else if (this.views.has(s) || this.templates.has(s)) nodes.add(s)
      // An instance of a template's class came or went.
      if (q.predicate.value === RDF_TYPE) for (const [tpl, cls] of this.templates) if (q.object.value === cls) nodes.add(tpl)
      for (const v of this.views.values()) {
        if (v.window === s) redefine.add(v.iri)
        if (v.def.node === s) redefine.add(v.iri)
      }
    }
    for (const node of nodes) {
      for (const v of this.views.values()) if (v.def.node === node) redefine.add(v.iri)
      if (this.store.match(this.store.factory.namedNode(node), this.store.factory.namedNode(RDF_TYPE), this.store.factory.namedNode(IV + 'View')).length) for (const d of this.definitions(node)) redefine.add(d.iri)
    }
    // A parameter changed: retune in place (LIMIT / OFFSET), else compile again.
    const retuned = new Set<string>()
    for (const v of this.views.values()) {
      if (redefine.has(v.iri) || !v.params.length) continue
      if (![...changes.add, ...changes.remove].some((q) => v.params.some((p) => p.reads(q)))) continue
      const params = this.resolve(v.def, v.paramNodes)
      if (params.every((p, i) => sameTerm(p.value, v.params[i]?.value))) continue
      const next = this.compileFor(v.def, params)
      if (next && v.retune(next, params, out, now, changes.remove)) retuned.add(v.iri)
      else redefine.add(v.iri)
    }
    for (const iri of redefine) {
      const old = this.views.get(iri)
      old?.drop(out)
      this.views.delete(iri)
    }
    if (redefine.size) {
      const defs = new Map<string, Definition>()
      for (const node of new Set([...nodes, ...[...redefine].map((i) => this.defNode(i)).filter((x): x is string => !!x)])) {
        if (!this.store.match(this.store.factory.namedNode(node), this.store.factory.namedNode(RDF_TYPE), this.store.factory.namedNode(IV + 'View')).length) continue
        for (const d of this.definitions(node)) if (redefine.has(d.iri)) defs.set(d.iri, d)
      }
      for (const d of defs.values()) this.define(d, out)
      this.sort()
    }
    let worked = false
    const viewDelta: Delta = { add: [], remove: [] }
    const applied: Delta = { add: [], remove: [] }
    const flush = () => {
      if (!out.size) return
      const d = this.sink(out)
      out.clear()
      applied.add.push(...d.add)
      applied.remove.push(...d.remove)
      viewDelta.add.push(...d.add)
      viewDelta.remove.push(...d.remove)
    }
    flush()
    for (const v of this.order) {
      if (v.suspended) continue
      if (v.whileShown && !this.demanded(v)) {
        v.suspended = true
        continue
      }
      if (redefine.has(v.iri) && !retuned.has(v.iri)) {
        out.plus(v.initial(now))
        flush()
        continue
      }
      // A view that reads other views' output sees what they wrote this tick.
      const own = v.readsViews ? { add: [...changes.add, ...viewDelta.add], remove: [...changes.remove, ...viewDelta.remove] } : changes
      const w = this.watchdog
      const t0 = w ? w.clock() : 0
      const d = v.step(own, now)
      if (w) w.note('view', v.iri, undefined, d?.size ?? 0, w.clock() - t0)
      if (d) out.plus(d), (worked = true)
      flush()
    }
    if (worked || redefine.size) {
      this.totals.ticks++
      this.totals.ms += performance.now() - t
    }
    this.check(now)
    this.reschedule()
    this.settled()
    return applied
  }

  private defNode(iri: string): string | undefined {
    if (this.store.match(this.store.factory.namedNode(iri), this.store.factory.namedNode(RDF_TYPE), this.store.factory.namedNode(IV + 'View')).length) return iri
    for (const tpl of this.templates.keys()) if (iri.endsWith('/~view-' + localName(tpl))) return tpl
    return undefined
  }

  /** Rebuild suspended views whose component is shown again; returns the view graph's change (applied). */
  resume(): Delta {
    const out = new ZSet<Quad>()
    const now = this.clock.now()
    const applied: Delta = { add: [], remove: [] }
    for (const v of this.order) {
      if (!v.suspended || !this.demanded(v)) continue
      v.suspended = false
      out.plus(v.rebuild(now))
      const d = this.sink(out)
      out.clear()
      applied.add.push(...d.add)
      applied.remove.push(...d.remove)
    }
    if (!applied.add.length && !applied.remove.length) return applied
    this.check(now)
    this.reschedule()
    this.settled()
    return applied
  }

  private demanded(v: MaintainedView): boolean {
    if (!v.whileShown || !this.shown) return true
    const f = this.store.factory
    const instances = this.store.match(null, f.namedNode(RDF_TYPE), f.namedNode(v.whileShown))
    return (instances.length ? instances.map((q) => q.subject.value) : [v.whileShown]).some((c) => this.shown!(c))
  }

  private hosted = new Map<string, Definition>()

  /**
   * A view a host effect defines rather than the graph (a persistence policy's CONSTRUCT): maintained
   * like any other, ephemeral (its output goes to subscribers and since(), never to the views graph),
   * a fallback to recompute expected. Defined now if the runtime has started, else when it starts.
   */
  host(iri: string, query: string, form: 'select' | 'construct' = 'construct'): ViewHandle | undefined {
    const def: Definition = { iri, node: iri, hosted: { query, form } }
    this.hosted.set(iri, def)
    if (!this.started) return undefined
    const out = new ZSet<Quad>()
    this.views.get(iri)?.drop(out)
    this.define(def, out)
    this.sort()
    const v = this.views.get(iri)
    if (v) out.plus(v.initial(this.clock.now()))
    this.sink(out)
    return v
  }

  /**
   * A rule maintained as a view (iv:strategy iv:Incremental on a rule): its CONSTRUCT, with ?this
   * (the focus) as the partition key, hosted; each output row belongs to its ?this. The engine reads a
   * focus's output from it and hears which foci changed. Undefined: the runtime has not started, or
   * the query cannot be maintained (the rule is evaluated as usual).
   */
  hostRule(iri: string, query: string): RuleView | undefined {
    const def: Definition = { iri, node: iri, hosted: { query, form: 'construct', attribute: 'this' } }
    if (!this.started || !this.store.sparql) return undefined
    const plan = this.compileFor(def, [])
    if (!plan || plan.kind !== 'maintained') return undefined
    this.hosted.set(iri, def)
    const out = new ZSet<Quad>()
    this.views.get(iri)?.drop(out)
    this.define(def, out)
    this.sort()
    const v = this.views.get(iri)!
    out.plus(v.initial(this.clock.now()))
    this.sink(out)
    return {
      view: v,
      quads: (focus) => v.quadsFor(focus),
      onChange: (fn) => v.onFoci(fn),
      dispose: () => this.unhost(iri),
    }
  }

  /** Stop maintaining a hosted view. */
  unhost(iri: string): void {
    this.hosted.delete(iri)
    const v = this.views.get(iri)
    if (!v) return
    const out = new ZSet<Quad>()
    v.drop(out)
    this.views.delete(iri)
    this.sort()
    this.sink(out)
  }

  /** Called after every tick (and start, and a resume) once every view has settled: replication pumps here. */
  afterTick(fn: (tick: number) => void): () => void {
    this.afterTicks.push(fn)
    return () => (this.afterTicks = this.afterTicks.filter((x) => x !== fn))
  }

  private settled() {
    for (const fn of this.afterTicks) fn(this.tickNo)
  }

  /** Stop: no window expiry fires any more (the store is being let go). */
  dispose(): void {
    this.timer?.cancel()
    this.timer = undefined
    this.onTimer = undefined
  }

  /** What every view holds (soak tests). */
  footprint(): Record<string, number> {
    const out: Record<string, number> = { views: this.views.size, refs: this.refs.size, terms: this.terms.size }
    for (const v of this.views.values()) for (const [k, n] of Object.entries(v.footprint())) out[k] = (out[k] ?? 0) + n
    return out
  }

  /**
   * The figures of views marked iv:measured, as triples on each view (idle time: the
   * runtime writes them; views never read the view graph, so this cannot feed back).
   * A from-scratch evaluation is timed at most every `sampleMs` for comparison.
   */
  figures(sampleMs = 1000): Delta {
    const f = this.store.factory
    const nn = (p: string) => f.namedNode(p)
    const now = this.clock.now()
    const add: Quad[] = []
    const remove: Quad[] = []
    const set = (s: NamedNode, p: string, o: Term) => {
      const old = this.store.match(s, nn(p), null, this.graph)
      if (old.length === 1 && old[0]!.object.equals(o)) return
      remove.push(...old)
      add.push(f.quad(s, nn(p), o, this.graph))
    }
    const int = (n: number) => f.literal(String(Math.round(n)), nn(NS.xsd + 'integer'))
    const dec = (n: number) => f.literal(n.toFixed(3), nn(NS.xsd + 'decimal'))
    for (const v of this.views.values()) {
      const s = nn(v.iri)
      if (this.store.match(nn(v.def.node), nn(IV + 'measured'), null).every((q) => q.object.value !== 'true')) continue
      set(s, IV + 'stateRows', int(v.stateRows()))
      set(s, IV + 'ticks', int(v.figures.ticks))
      set(s, IV + 'lastRows', int(v.figures.lastRows))
      set(s, IV + 'lastMs', dec(v.figures.lastMs))
      set(s, IV + 'meanMs', dec(v.figures.ticks ? v.figures.ms / v.figures.ticks : 0))
      if (v.lateMembers() !== undefined) set(s, IV + 'lateMembers', int(v.lateMembers()!))
      if (this.store.sparql && (v.sampledAt === undefined || now - v.sampledAt >= sampleMs)) {
        v.sampledAt = now
        const t = performance.now()
        const n = v.fromScratch(now).length
        set(s, IV + 'scratchMs', dec(performance.now() - t))
        set(s, IV + 'scratchRows', int(n))
        set(s, IV + 'inputRows', int(v.inputRows()))
        // The maintained result against that run: the check a page can show (✓ / ✕).
        set(s, IV + 'resultRows', int(v.plan.form === 'select' ? v.rows().length : v.quads().length))
        set(s, IV + 'matches', f.literal(String(v.compare(now) === undefined), nn(NS.xsd + 'boolean')))
        set(s, IV + 'checkedTick', int(this.tickNo))
      }
    }
    return { add, remove }
  }

  /** The view node's annotations. */
  private one(node: Term, p: string): Term | undefined {
    return this.store.match(node as NamedNode, this.store.factory.namedNode(p), null)[0]?.object
  }

  /** Parameters (iv:parameter) of a view, resolved for its focus. */
  private resolve(def: Definition, nodes: Term[]): Param[] {
    const f = this.store.factory
    return nodes.map((n) => {
      const name = this.one(n, IV + 'variable')?.value ?? ''
      const select = this.one(n, IV + 'select')?.value
      const property = this.one(n, IV + 'property')
      const from = this.one(n, IV + 'from') ?? (def.focus ? f.namedNode(def.focus) : undefined)
      const fallback = this.one(n, IV + 'default')
      let value: Term | undefined
      let reads: Param['reads'] = () => false
      if (select) {
        const text = def.focus ? select.replaceAll('$this', `<${def.focus}>`) : select
        // What it reads: the triple patterns of its query (anything, when they cannot be read).
        try {
          const deps = new Dependencies(f, parseQuery(text, prefixMap(this.prefixes())).select.where, [])
          reads = deps.problem ? () => true : (q) => deps.route(q).length > 0
        } catch {
          reads = () => true
        }
        const row = this.store.sparql ? this.store.select(this.prefixes() + '\n' + text, this.datasetFor(true) as never)[0] : undefined
        value = row?.get('value') ?? (row ? [...row.values()][0] : undefined)
      } else if (property && from) {
        reads = (q) => q.subject.equals(from) && q.predicate.equals(property)
        value = this.store.match(from as NamedNode, property as NamedNode, null)[0]?.object
      }
      return { name, value: value ?? fallback, reads }
    })
  }

  private compileFor(def: Definition, params: Param[]): ViewPlan | undefined {
    // A store without SPARQL runs no SPARQL plan, not even a recompute.
    if (!this.store.sparql) return undefined
    const f = this.store.factory
    const s = f.namedNode(def.node)
    const select = def.hosted ? (def.hosted.form === 'select' ? def.hosted.query : undefined) : this.one(s, SEM.select)?.value
    const construct = def.hosted ? (def.hosted.form === 'construct' ? def.hosted.query : undefined) : this.one(s, SEM.construct)?.value
    let query = construct ?? select ?? ''
    if (def.focus) query = query.replaceAll('$this', `<${def.focus}>`)
    for (const p of [...params].sort((a, b) => b.name.length - a.name.length)) {
      if (!p.value) return { kind: 'recompute', form: construct ? 'construct' : 'select', query, reason: `the parameter $${p.name} has no value (and no iv:default)` }
      query = query.replaceAll('$' + p.name, p.value.termType === 'Literal' && /integer$/.test(p.value.datatype.value) ? p.value.value : sparqlTerm(p.value))
    }
    const window = this.readWindow(def)
    const partitionBy = this.one(s, IV + 'partitionBy')?.value
    const source: ViewSource = {
      iri: def.iri,
      form: construct ? 'construct' : 'select',
      query,
      prefixes: this.prefixes(),
      over: this.one(s, IV + 'over')?.value,
      partitionBy: def.hosted?.attribute ? [def.hosted.attribute] : partitionBy ? partitionBy.split(/[\s,]+/).map((v) => v.replace(/^[?$]/, '')).filter(Boolean) : undefined,
      window,
      graphs: this.datasetFor(this.store.match(s, f.namedNode(IV + 'reads'), null).length > 0).filter((g) => g.termType === 'NamedNode').map((g) => g.value),
    }
    if (!source.query) return undefined
    return (this.opts.compiler ?? compiler).compile(source, this.runner(this.store.match(s, f.namedNode(IV + 'reads'), null).length > 0))
  }

  /**
   * A view's iv:window as declared: iv:SlidingWindow (iv:range), iv:TumblingWindow (iv:size,
   * iv:keep, iv:bucketVariable), iv:CountWindow (ldes:amount), each with iv:timeBy and iv:clock
   * (iv:WallClock, or iv:EventTime with iv:lateness). A window that says too little is ignored, with a warning.
   */
  private readWindow(def: Definition): WindowSource | undefined {
    const f = this.store.factory
    const node = this.one(f.namedNode(def.node), IV + 'window')
    if (!node) return undefined
    const types = new Set(this.store.match(node as NamedNode, f.namedNode(RDF_TYPE), null).map((q) => q.object.value))
    const duration = (p: string) => {
      const v = this.one(node, p)?.value
      return v ? parseDuration(v) : undefined
    }
    const timeBy = this.one(node, IV + 'timeBy')?.value
    const eventTime = this.one(node, IV + 'clock')?.value === IV + 'EventTime'
    const clock = { timeBy, eventTime: eventTime || undefined, latenessMs: eventTime ? (duration(IV + 'lateness') ?? 0) : undefined }
    if (types.has(IV + 'CountWindow')) {
      const amount = Number(this.one(node, NS.ldes + 'amount')?.value)
      if (Number.isInteger(amount) && amount >= 0) return { kind: 'count', amount, timeBy }
      warn(`<${def.iri}>: an iv:CountWindow needs an ldes:amount`)
      return undefined
    }
    if (types.has(IV + 'TumblingWindow')) {
      const sizeMs = duration(IV + 'size')
      const keep = Number(this.one(node, IV + 'keep')?.value ?? 1)
      if (sizeMs && Number.isInteger(keep) && keep > 0) return { kind: 'tumbling', sizeMs, keep, bucketVar: this.one(node, IV + 'bucketVariable')?.value.replace(/^[?$]/, ''), ...clock }
      warn(`<${def.iri}>: an iv:TumblingWindow needs an iv:size duration (and a positive iv:keep)`)
      return undefined
    }
    const rangeMs = duration(IV + 'range')
    if (rangeMs !== undefined) return { kind: 'sliding', rangeMs, ...clock }
    warn(`<${def.iri}>: iv:window needs an iv:range duration`)
    return undefined
  }

  private define(def: Definition, out: ZSet<Quad>) {
    const f = this.store.factory
    const s = f.namedNode(def.node)
    const fn = this.one(s, SEM.function)?.value
    // A hosted view is expected to fall back now and then (its query is someone else's): iv:Auto.
    const strategy = def.hosted ? IV + 'Auto' : this.one(s, IV + 'strategy')?.value
    const windowNode = this.one(s, IV + 'window')
    const paramNodes = this.store.match(s, f.namedNode(IV + 'parameter'), null).map((q) => q.object)
    const params = this.resolve(def, paramNodes)
    const fnView = fn ? this.opts.functions?.[fn] : undefined
    if (fn && !fnView) throw new Error(`No view function registered for "${fn}" (<${def.iri}>)`)
    const hasQuery = !!def.hosted || !!(this.one(s, SEM.construct) ?? this.one(s, SEM.select))
    const sparqlPlan = this.compileFor(def, params)
    const useFn = fnView && (!this.store.sparql || this.prefer === 'function' || !hasQuery)
    let plan: ViewPlan
    if (useFn) plan = this.fnPlan(fnView!, def, sparqlPlan)
    else if (sparqlPlan) plan = sparqlPlan
    else {
      warn(`<${def.iri}> has no implementation this store can run; skipped`)
      return
    }
    if (plan.kind === 'recompute') {
      const note = `<${def.iri}> is recomputed from scratch: ${plan.reason}`
      // iv:Auto says a fallback is expected (a query typed into the workbench): no error, no warning; strict still compares it.
      if (strategy === IV + 'Incremental' || (this.strict && strategy !== IV + 'Auto')) throw new Error(note)
      if (strategy !== IV + 'Auto') warn(note)
    }
    const view = new MaintainedView(this, def, plan, windowNode?.termType === 'NamedNode' ? windowNode.value : undefined)
    view.params = params
    view.paramNodes = paramNodes
    view.whileShown = this.one(s, IV + 'whileShown')?.value ?? def.focus
    view.deltaLog = this.one(s, IV + 'deltaLog')?.value
    if (view.deltaLog) view.keepHistory(this.retained(view.deltaLog) ?? 12)
    // iv:ResultSet: a SELECT's rows in the views graph, a CONSTRUCT's triples nowhere (a query typed into the workbench).
    const materialize = this.one(s, IV + 'materialize')?.value
    view.materialize = materialize === IV + 'Graph' || materialize === IV + 'ResultSet'
    view.ephemeral = !!def.hosted || materialize === IV + 'Ephemeral' || (materialize === IV + 'ResultSet' && plan.form === 'construct')
    view.reads = this.store.match(s, f.namedNode(IV + 'reads'), null).map((q) => q.object.value)
    view.readsViews = view.reads.length > 0
    this.views.set(def.iri, view)
    out.plus(view.planQuads())
  }

  /** Step order: views that read other views (iv:reads) after them. */
  private sort() {
    const done = new Set<MaintainedView>()
    const order: MaintainedView[] = []
    const visit = (v: MaintainedView, depth: number) => {
      if (done.has(v) || depth > 32) return
      done.add(v)
      for (const r of v.reads) for (const w of this.views.values()) if (w !== v && (w.iri === r || w.def.node === r)) visit(w, depth + 1)
      order.push(v)
    }
    for (const v of this.views.values()) visit(v, 0)
    this.order = order
  }

  /** A function view's plan: the function's evaluators, the SPARQL view's oracle if it has one. */
  private fnPlan(fn: FnView, def: Definition, sparql: ViewPlan | undefined): MaintainedPlan {
    const f = this.store.factory
    const declared = this.readWindow(def)
    const timeBy = declared ? (declared.timeBy ?? (fn.stream ? this.runner().timestampPath(fn.stream) : undefined)) : undefined
    const keys = fn.streamVar ? [fn.streamVar, fn.memberVar] : [fn.memberVar]
    const slots = [...keys, ...fn.groupBy.filter((g) => !keys.includes(g)), ...fn.aggregates.map((a) => a.slot).filter((s) => s !== '*' && !keys.includes(s) && !fn.groupBy.includes(s))]
    const part: Part = {
      keys,
      slots: [...new Set(slots)],
      stream: { stream: fn.stream, streamVar: fn.streamVar, memberVar: fn.memberVar },
      window: declared && timeBy && fn.stream ? { ...declared, stream: fn.stream, timeBy } : undefined,
      local: (batch) => {
        const out = new Map<string, Map<string, Term>[]>()
        const members = batch ? batch.map((k) => ({ stream: (fn.streamVar ? k[0] : f.namedNode(fn.stream!)) as NamedNode, member: k[k.length - 1] as NamedNode })) : this.members(fn.stream)
        for (const { stream, member } of members) {
          const rows = fn.local(this.ctx, stream, member)
          for (const r of rows) {
            r.set(fn.memberVar, member)
            if (fn.streamVar) r.set(fn.streamVar, stream)
          }
          if (rows.length) out.set(keyString(fn.streamVar ? [stream, member] : [member]), rows)
        }
        return out
      },
      op: { k: 'group', groupBy: fn.groupBy, aggregates: fn.aggregates },
      vars: [...fn.groupBy, ...fn.aggregates.map((a) => a.as)],
      algebra: { local: `function ${fn.local.name || 'local'}`, op: `Group((${fn.groupBy.map((v) => '?' + v).join(' ')}), ${fn.aggregates.map((a) => `${a.fn} AS ?${a.as}`).join(', ')})` },
    }
    return {
      kind: 'maintained',
      form: this.one(f.namedNode(def.node), SEM.construct) ? 'construct' : 'select',
      parts: [part],
      vars: part.vars,
      output: fn.output ? (row) => fn.output!(this.ctx, row).map((q) => f.quad(q.subject, q.predicate, q.object, this.graph)) : undefined,
      reference: sparql?.kind === 'maintained' ? sparql.reference : sparql?.kind === 'recompute' ? () => sparql.query : undefined,
      algebra: { output: fn.output ? 'function' : undefined },
      signature: 'function',
    }
  }

  /** The graphs a view reads: the input graphs, and the views graph for views that read other views. */
  datasetFor(readsViews: boolean): Term[] {
    return readsViews ? [...this.graphs, this.graph] : this.graphs
  }

  runner(readsViews = false) {
    const prefixes = this.prefixes()
    const f = this.store.factory
    const graphs = this.datasetFor(readsViews)
    return {
      factory: f,
      select: (q: string) => this.store.select(prefixes + '\n' + q, graphs as never),
      construct: (q: string) => this.store.construct(prefixes + '\n' + q, graphs as never).map((x) => f.quad(x.subject, x.predicate, x.object, this.graph)),
      timestampPath: (stream: string) => this.store.match(f.namedNode(stream), f.namedNode(NS.ldes + 'timestampPath'), null)[0]?.object.value,
    }
  }

  /**
   * Triple lookups over a view's graphs with this tick's removed triples put back: the old and
   * the new state together, for dependency probes.
   */
  lookup(removed: readonly Quad[], readsViews: boolean): Lookup {
    const graphs = this.datasetFor(readsViews)
    const by = new Map<string, Quad[]>()
    const index = (k: string, q: Quad) => {
      let l = by.get(k)
      if (!l) by.set(k, (l = []))
      l.push(q)
    }
    for (const q of removed) {
      index('s' + termKey(q.subject), q)
      index('p' + q.predicate.value, q)
      index('o' + termKey(q.object), q)
    }
    // Without `g`: the union default graph (a triple once). With it: quads of the named graphs
    // (`g` one of them, or null: any), each with its graph.
    return (s, p, o, g) => {
      const seen = new Set<string>()
      const out: Quad[] = []
      const named = g !== undefined
      const put = (q: Quad) => {
        if (named && (q.graph.termType !== 'NamedNode' || (g && !q.graph.equals(g)))) return
        const k = termKey(q.subject) + ' ' + q.predicate.value + ' ' + termKey(q.object) + (named ? ' ' + q.graph.value : '')
        if (!seen.has(k)) seen.add(k), out.push(q)
      }
      for (const x of graphs) if (!named || (x.termType === 'NamedNode' && (!g || x.equals(g)))) for (const q of this.store.match(s as never, p as never, o as never, x as never)) put(q)
      if (removed.length) {
        const pool = s ? by.get('s' + termKey(s)) : o ? by.get('o' + termKey(o)) : p ? by.get('p' + p.value) : removed
        for (const q of pool ?? []) if ((!s || q.subject.equals(s)) && (!p || q.predicate.equals(p)) && (!o || q.object.equals(o))) put(q)
      }
      return out
    }
  }

  /** A stream's retention (tree:view/ldes:retentionPolicy/ldes:amount), if it has one. */
  retained(stream: string): number | undefined {
    const f = this.store.factory
    const one = (s: Term, p: string) => this.store.match(s as NamedNode, f.namedNode(p), null)[0]?.object
    const view = one(f.namedNode(stream), NS.tree + 'view')
    const policy = view && one(view, NS.ldes + 'retentionPolicy')
    const amount = policy && one(policy, NS.ldes + 'amount')
    return amount ? Number(amount.value) : undefined
  }

  /** Members of a stream (or of every stream) now, in the views' dataset. */
  members(stream: string | undefined, member?: string): { stream: NamedNode; member: NamedNode }[] {
    const f = this.store.factory
    const out: { stream: NamedNode; member: NamedNode }[] = []
    const seen = new Set<string>()
    for (const g of this.graphs) {
      for (const q of this.store.match(stream ? f.namedNode(stream) : null, f.namedNode(TREE_MEMBER), member ? f.namedNode(member) : null, g as never)) {
        if (q.object.termType !== 'NamedNode' || q.subject.termType !== 'NamedNode') continue
        const k = q.subject.value + ' ' + q.object.value
        if (!seen.has(k)) seen.add(k), out.push({ stream: q.subject, member: q.object })
      }
    }
    return out
  }

  timestamp(member: NamedNode, timeBy: string): number | undefined {
    for (const g of this.graphs) {
      const t = this.store.match(member, this.store.factory.namedNode(timeBy), null, g as never)[0]?.object
      if (!t) continue
      const n = Number(t.value)
      return Number.isNaN(n) ? Date.parse(t.value) : n
    }
    return undefined
  }

  /** Apply a change of view output (weights) to the store through reference counts. */
  private sink(out: ZSet<Quad>): Delta {
    const add: Quad[] = []
    const remove: Quad[] = []
    const f = this.store.factory
    for (const [k, q0, w] of out.entries()) {
      const q = q0.graph.equals(this.graph) ? q0 : f.quad(q0.subject, q0.predicate, q0.object, this.graph)
      const ref = this.refs.get(k)
      const n = (ref?.n ?? 0) + w
      if (n < 0) throw new Error(`view output went negative for ${k}`)
      if (!ref && n > 0) {
        this.refs.set(k, { q, n })
        if (!this.store.has(q)) this.store.add(q), add.push(q)
      } else if (ref && n === 0) {
        this.refs.delete(k)
        if (this.store.has(ref.q)) this.store.delete(ref.q), remove.push(ref.q)
      } else if (ref) ref.n = n
    }
    let state = 0
    for (const v of this.views.values()) state += v.stateRows()
    this.totals.state = state
    return { add, remove }
  }

  /** Strict mode: every view equals its from-scratch evaluation. */
  private check(now: number) {
    if (!this.strict || !this.store.sparql) return
    for (const v of this.views.values()) {
      if (v.suspended) continue
      const problem = v.compare(now)
      if (problem) throw new Error(`View <${v.iri}> differs from a from-scratch evaluation: ${problem}`)
    }
  }

  private reschedule() {
    let next: number | undefined
    for (const v of this.views.values()) {
      const at = v.nextExpiry()
      if (at !== undefined && (next === undefined || at < next)) next = at
    }
    if (next === this.timer?.at) return
    this.timer?.cancel()
    this.timer = undefined
    if (next === undefined) return
    const at = next
    this.timer = {
      at,
      cancel: this.clock.schedule(at, () => {
        if (this.timer?.at === at) this.timer = undefined
        this.onTimer?.()
      }),
    }
  }

  /** The view runtime's context (function views read through it). */
  get context(): RuleContext {
    return this.ctx
  }
}

const sameTerm = (a: Term | undefined, b: Term | undefined) => (!a && !b) || (!!a && !!b && a.equals(b))

/** A partition's cached contribution: its rows (as interned ids), and whether they count (inside the window). */
interface PartitionState {
  key: Term[]
  rows: number[][]
  inWindow: boolean
}

type Row = Map<string, Term>

const rowKey = (ids: readonly number[]) => ids.join(',')

/** What happened in a tick, for the delta log. */
interface Happened {
  appended: number
  retired: number
  changed: number
  expired: number
  groups: { key: string; before?: Term; after?: Term }[]
  rowsIn: number
  rowsOut: number
}

/** One part's state: its partitions, and what its operator holds. */
class PartState {
  partitions = new Map<string, PartitionState>()
  part: Part
  /** Stream parts without exact dependencies: member IRI -> its partition keys (for the member closure). */
  byMember = new Map<string, Set<string>>()
  partitionRows = 0
  groups = new Map<string, Group>()
  /** GROUP BY: each group's emitted row. */
  emitted = new Map<string, Row>()
  /** Bag / DISTINCT: output rows with their weight. */
  counts = new Map<string, { row: Row; ids: number[]; n: number }>()
  /** The window (window.ts): which of the stream's members count now. */
  win?: WindowState
  post: PostCache
  slotIndex: Map<string, number>
  /** The derived subqueries' own states (Part.derived), stepped before this part. */
  inner: PartState[]

  constructor(
    part: Part,
    readonly view: MaintainedView,
  ) {
    this.part = part
    this.slotIndex = new Map(part.slots.map((s, i) => [s, i]))
    this.post = new PostCache(view, part.post)
    this.inner = (part.derived ?? []).map((d) => new PartState(d.part, view))
    if (part.window) this.win = new WindowState(part.window, view.rt.store, () => view.rt.datasetFor(view.readsViews))
  }

  /** The derived subqueries' current rows (what the body's evaluation inlines). */
  private derivedRows(): Map<string, Term>[][] | undefined {
    if (!this.inner.length) return undefined
    return this.inner.map((ps) => [...ps.current().entries()].flatMap(([, r, w]) => Array.from({ length: w }, () => r)))
  }

  /**
   * Step the derived subqueries with this tick's change; the partitions their changed rows reach
   * (merged into `into`), or 'all'.
   */
  stepDerived(changes: Delta, now: number, happened: Happened, into: Map<string, (Term | undefined)[]>): 'all' | undefined {
    let all = false
    this.inner.forEach((ps, i) => {
      const touched = changes.add.length || changes.remove.length ? ps.touched(changes) : new Map<string, (Term | undefined)[]>()
      // A derived part's own derived parts step inside its update.
      const nested = ps.inner.length ? ps.stepDerived(changes, now, happened, touched === 'all' ? new Map() : touched) : undefined
      if (touched !== 'all' && nested !== 'all' && !touched.size) return
      const d = ps.update(touched === 'all' || nested === 'all' ? undefined : touched, now, happened)
      if (all || !d.size) return
      const lookup = this.rt.lookup(changes.remove, this.view.readsViews)
      for (const [, row] of d.entries()) {
        const keys = this.part.deps!.probeDerived(i, row, lookup)
        if (keys === 'global') return void (all = true)
        for (const k of keys) into.set(keyString(k), k)
      }
    })
    return all ? 'all' : undefined
  }

  get rt() {
    return this.view.rt
  }

  /**
   * The same part with other constants (a parameter changed; the structure is the same): the
   * partitions that may differ - those the changed patterns match, before and after - or 'all'
   * when the change is elsewhere in the body (a FILTER). Derived parts are evaluated anew here.
   */
  retarget(next: Part, now: number, happened: Happened, removed: readonly Quad[]): Map<string, (Term | undefined)[]> | 'all' {
    const old = this.part
    this.part = next
    let all = false
    this.inner.forEach((ps, i) => {
      if (ps.retarget(next.derived![i]!.part, now, happened, removed) === 'all' || ps.part.algebra.local !== old.derived![i]!.part.algebra.local) {
        ps.update(undefined, now, happened)
        all = true
      }
    })
    if (all) return 'all'
    const touched = new Map<string, (Term | undefined)[]>()
    // An expression over the rows changed (a parameter in an aggregate's argument): every partition.
    if (old.head !== next.head) return 'all'
    if (old.algebra.local === next.algebra.local) return touched
    if (!old.deps || !next.deps || old.patternless !== next.patternless || old.deps.patterns.length !== next.deps.patterns.length) return 'all'
    const lookup = this.rt.lookup(removed, this.view.readsViews)
    const constant = (x: import('./deps.ts').Pos | { path: unknown }) => ('t' in x ? x.t : null)
    for (let j = 0; j < old.deps.patterns.length; j++) {
      const a = old.deps.patterns[j]!
      const b = next.deps.patterns[j]!
      if (JSON.stringify([a.s, a.p, a.o]) === JSON.stringify([b.s, b.p, b.o])) continue
      for (const [deps, pat] of [[old.deps, a], [next.deps, b]] as const) {
        if ('path' in pat.p) return 'all'
        for (const q of lookup(constant(pat.s), constant(pat.p), constant(pat.o))) {
          const keys = deps.probe(pat, q, lookup)
          if (keys === 'global') return 'all'
          for (const k of keys) touched.set(keyString(k), k)
        }
      }
    }
    return touched
  }

  /** Every partition anew (start, rebuild). */
  all(now: number, happened: Happened): ZSet<Row> {
    this.win?.load()
    for (const ps of this.inner) ps.all(now, happened)
    return this.update(undefined, now, happened)
  }

  /**
   * The partitions this tick's change touches (by key string; a key with undefined parts is a
   * slice: every partition that agrees with the rest); 'all' when it reaches every one.
   */
  touched(changes: Delta): Map<string, (Term | undefined)[]> | 'all' {
    const p = this.part
    const touched = new Map<string, (Term | undefined)[]>()
    if (p.deps) {
      const lookup = this.rt.lookup(changes.remove, this.view.readsViews)
      const candidates = new Map<string, (Term | undefined)[]>()
      for (const q of [...changes.remove, ...changes.add]) {
        for (const pat of p.deps.route(q)) {
          const keys = p.deps.probe(pat, q, lookup)
          if (keys === 'global') return 'all'
          for (const k of keys) candidates.set(keyString(k), k)
        }
      }
      // A key that had no rows must now match the patterns over the key alone to have any.
      const anchors = p.deps.anchors()
      for (const [k, key] of candidates) {
        if (this.partitions.has(k) || key.some((t) => !t) || anchors.every((a) => this.anchored(a, key as Term[]))) touched.set(k, key)
      }
      return touched
    }
    // A stream part written as functions: membership, or a triple of the member or of a node minted under it.
    const s = p.stream!
    for (const q of [...changes.remove, ...changes.add]) {
      if (q.predicate.value === TREE_MEMBER && q.object.termType === 'NamedNode' && (!s.stream || q.subject.value === s.stream)) {
        const key = s.streamVar ? [q.subject, q.object] : [q.object]
        touched.set(keyString(key), key)
        continue
      }
      if (q.subject.termType !== 'NamedNode') continue
      for (let x = q.subject.value; ; ) {
        for (const k of this.byMember.get(x) ?? []) touched.set(k, this.partitions.get(k)!.key)
        const o = owningFocus(x)
        if (o === x) break
        x = o
      }
    }
    return touched
  }

  private anchored(a: import('./deps.ts').Pat, key: Term[]): boolean {
    const keys = this.part.keys
    const val = (pos: { v: string } | { t: Term }) => ('t' in pos ? pos.t : key[keys.indexOf(pos.v)])
    const s = val(a.s)
    const pp = val(a.p as { v: string } | { t: Term })
    const o = val(a.o)
    for (const g of this.rt.datasetFor(this.view.readsViews)) if (this.rt.store.match((s ?? null) as never, (pp ?? null) as never, (o ?? null) as never, g as never).length) return true
    return false
  }

  /** Partitions by one part of their key: position -> term key -> partition keys (keys of more than one variable). */
  private byPosition: Map<string, Set<string>>[] = []

  private index(key: string, terms: Term[], w: 1 | -1) {
    if (terms.length < 2) return
    terms.forEach((t, i) => {
      const m = (this.byPosition[i] ??= new Map())
      const k = termKey(t)
      let set = m.get(k)
      if (w > 0) {
        if (!set) m.set(k, (set = new Set()))
        set.add(key)
      } else if (set) {
        set.delete(key)
        if (!set.size) m.delete(k)
      }
    })
  }

  /** The cached partitions a slice (a key with undefined parts) covers. */
  private slice(key: (Term | undefined)[]): string[] {
    const at = key.findIndex((t) => !!t)
    if (at < 0) return [...this.partitions.keys()]
    const candidates = this.byPosition[at]?.get(termKey(key[at]!)) ?? new Set<string>()
    return [...candidates].filter((k) => {
      const terms = this.partitions.get(k)!.key
      return key.every((t, i) => !t || t.equals(terms[i]!))
    })
  }

  /** Evaluate partitions (all, or those keyed, or slices of them) and push their change through the operator. */
  update(keys: Map<string, (Term | undefined)[]> | undefined, now: number, happened: Happened): ZSet<Row> {
    const p = this.part
    const terms = this.rt.terms
    const f = this.rt.store.factory
    const out = new ZSet<Row>()
    const dirty = new Set<string>()
    const enter = (st: PartitionState, w: 1 | -1) => {
      for (const r of st.rows) this.enter(r, w, dirty, out)
      happened.rowsIn += st.rows.length
    }
    const release = (st: PartitionState) => {
      for (const r of st.rows) for (const id of r) terms.release(id)
    }
    let evaluated: Map<string, Row[]>
    let visit: Set<string>
    const derived = this.derivedRows()
    // A key VALUES cannot hold (a blank node): every partition, once.
    if (keys && [...keys.values()].some((k) => k.some((t) => t?.termType === 'BlankNode'))) keys = undefined
    if (keys === undefined) {
      evaluated = p.local(undefined, derived)
      visit = new Set([...this.partitions.keys(), ...evaluated.keys()])
    } else {
      let batch = [...keys.values()]
      // Function parts: only current members are evaluated (a retired member has no rows).
      if (!p.deps && p.stream) batch = batch.filter((k) => this.rt.members(p.stream!.streamVar ? k[0]!.value : p.stream!.stream, k[k.length - 1]!.value).length)
      evaluated = batch.length ? p.local(batch, derived) : new Map()
      visit = new Set()
      for (const [k, key] of keys) {
        if (key.every((t) => !!t)) visit.add(k)
        else for (const c of this.slice(key)) visit.add(c)
      }
      for (const k of evaluated.keys()) visit.add(k)
    }
    this.view.rt.totals.partitions += keys === undefined ? evaluated.size : visit.size
    this.view.figures.partitions += keys === undefined ? evaluated.size : visit.size
    // The window moves first: which members count now, and which changed side.
    const win = this.win
    const flips = win ? win.reconcile(now) : undefined
    const memberOf = (key: Term[]) => key[key.length - 1]!.value
    for (const key of visit) {
      const old = this.partitions.get(key)
      const found = evaluated.get(key)
      let next: PartitionState | undefined
      if (found?.length) {
        const keyTerms = old?.key ?? p.keys.map((k) => found[0]!.get(k)!)
        const member = win ? memberOf(keyTerms) : undefined
        // A tumbling window's bucket is a column the window binds, not the body.
        const bucket = win && p.window!.bucketVar ? win.bucket(member!) : undefined
        if (bucket) for (const row of found) row.set(p.window!.bucketVar!, bucket)
        const ids = found.map((row) => p.slots.map((s) => terms.id(row.get(s))))
        next = { key: keyTerms, rows: ids, inWindow: !win || win.isIn(member!) }
        if (old && old.inWindow === next.inWindow && sameRows(old.rows, ids)) continue
        for (const r of ids) for (const id of r) terms.retain(id)
      }
      if (!old && !next) continue
      happened[old && next ? 'changed' : next ? 'appended' : 'retired']++
      if (old?.inWindow) enter(old, -1)
      if (old) release(old), (this.partitionRows -= old.rows.length)
      if (next) {
        if (!old && (!p.deps || win)) {
          const m = next.key[next.key.length - 1]!.value
          let ks = this.byMember.get(m)
          if (!ks) this.byMember.set(m, (ks = new Set()))
          ks.add(key)
        }
        this.partitionRows += next.rows.length
        if (!old) this.index(key, next.key, 1)
        this.partitions.set(key, next)
        if (next.inWindow) enter(next, 1)
      } else if (old) {
        this.partitions.delete(key)
        this.index(key, old.key, -1)
        if (!p.deps || win) {
          const m = old.key[old.key.length - 1]!.value
          const ks = this.byMember.get(m)
          ks?.delete(key)
          if (!ks?.size) this.byMember.delete(m)
        }
      }
    }
    // Members that changed side without their partition changing: in, or out (aged out, pushed out by newer ones).
    if (flips)
      for (const m of flips)
        for (const key of this.byMember.get(m) ?? []) {
          if (visit.has(key)) continue
          const st = this.partitions.get(key)!
          const inside = win!.isIn(m)
          if (st.inWindow === inside) continue
          enter(st, inside ? 1 : -1)
          st.inWindow = inside
          happened[inside ? 'appended' : 'expired']++
        }
    terms.sweep()
    if (p.op.k === 'group') this.groupOutput(dirty, out, happened, f)
    return this.post.map(out)
  }

  /** A partition row enters (w = 1) or leaves (-1) the operator. */
  private enter(r: number[], w: 1 | -1, dirty: Set<string>, out: ZSet<Row>) {
    const p = this.part
    const terms = this.rt.terms
    const op = p.op
    if (op.k === 'group') {
      const gIds = op.groupBy.map((v) => r[this.slotIndex.get(v)!]!)
      const gk = rowKey(gIds)
      let g = this.groups.get(gk)
      if (!g) {
        gIds.forEach((id) => terms.retain(id))
        this.groups.set(gk, (g = newGroup(terms, gIds, op.aggregates)))
      }
      g.rows += w
      op.aggregates.forEach((a, i) => accumulate(terms, a, g!.accs[i]!, a.slot === '*' ? 0 : r[this.slotIndex.get(a.slot)!]!, w))
      dirty.add(gk)
      return
    }
    // Bag and DISTINCT: the row projected on the part's variables.
    const ids = p.vars.map((v) => r[this.slotIndex.get(v)!]!)
    const k = rowKey(ids)
    let c = this.counts.get(k)
    if (!c) {
      const row: Row = new Map()
      p.vars.forEach((v, i) => {
        const t = terms.term(ids[i]!)
        if (t) row.set(v, t)
      })
      ids.forEach((id) => terms.retain(id))
      this.counts.set(k, (c = { row, ids, n: 0 }))
    }
    const before = c.n
    c.n += w
    if (op.k === 'bag') out.add(mapKey(c.row), c.row, w)
    else if (before === 0 && c.n > 0) out.add(mapKey(c.row), c.row, 1)
    else if (before > 0 && c.n === 0) out.add(mapKey(c.row), c.row, -1)
    if (c.n === 0) {
      c.ids.forEach((id) => terms.release(id))
      this.counts.delete(k)
    }
  }

  /** The groups that changed: their old rows out, their new rows in. */
  private groupOutput(dirty: Set<string>, out: ZSet<Row>, happened: Happened, f: import('../store/types.ts').Factory) {
    const op = this.part.op as Extract<Part['op'], { k: 'group' }>
    const terms = this.rt.terms
    // Every query with aggregates and no GROUP BY has its one group, even empty (COUNT 0).
    if (!op.groupBy.length && !this.groups.has('')) {
      this.groups.set('', newGroup(this.rt.terms, [], op.aggregates))
      dirty.add('')
    }
    for (const gk of dirty) {
      const g = this.groups.get(gk)!
      const before = this.emitted.get(gk)
      let row: Row | undefined
      if (g.rows > 0 || !op.groupBy.length) {
        row = new Map()
        op.groupBy.forEach((v, i) => {
          const t = terms.term(g.ids[i]!)
          if (t) row!.set(v, t)
        })
        op.aggregates.forEach((a, i) => {
          const t = result(f, terms, a, g.accs[i]!)
          if (t) row!.set(a.as, t)
        })
      } else {
        g.ids.forEach((id) => terms.release(id))
        this.groups.delete(gk)
      }
      const rk = row ? mapKey(row) : undefined
      if ((before && rk === mapKey(before)) || (!before && !row)) continue
      const first = op.aggregates.find((a) => !a.as.startsWith('__'))?.as ?? op.aggregates[0]?.as
      happened.groups.push({ key: groupLabel(op.groupBy.map((v) => (row ?? before!).get(v))) || first || '', before: first ? before?.get(first) : undefined, after: first ? row?.get(first) : undefined })
      if (before) out.add(mapKey(before), before, -1), this.emitted.delete(gk)
      if (row) out.add(rk!, row, 1), this.emitted.set(gk, row)
    }
  }

  /** The part's current output (its integral). */
  current(): ZSet<Row> {
    const z = new ZSet<Row>()
    if (this.part.op.k === 'group') for (const r of this.emitted.values()) z.add(mapKey(r), r, 1)
    else for (const c of this.counts.values()) z.add(mapKey(c.row), c.row, this.part.op.k === 'bag' ? c.n : 1)
    return this.post.current(z)
  }

  reset() {
    for (const ps of this.inner) ps.reset()
    const terms = this.rt.terms
    for (const st of this.partitions.values()) for (const r of st.rows) for (const id of r) terms.release(id)
    for (const g of this.groups.values()) for (const id of g.ids) terms.release(id)
    for (const c of this.counts.values()) for (const id of c.ids) terms.release(id)
    this.partitions.clear()
    this.byPosition = []
    this.byMember.clear()
    this.partitionRows = 0
    this.groups.clear()
    this.emitted.clear()
    this.counts.clear()
    this.win?.clear()
    this.post.clear()
  }

  stateRows(): number {
    return this.partitionRows + this.groups.size + this.counts.size + (this.win?.size ?? 0) + this.post.size + this.inner.reduce((n, ps) => n + ps.stateRows(), 0)
  }

  footprint(): Record<string, number> {
    let acc = 0
    for (const g of this.groups.values()) for (const a of g.accs) acc += accSize(a)
    const out: Record<string, number> = { members: this.partitions.size, byMember: this.byMember.size, memberRows: this.partitionRows, groups: this.groups.size, accumulators: acc, counts: this.counts.size, window: this.win?.size ?? 0, post: this.post.size }
    for (const ps of this.inner) for (const [k, n] of Object.entries(ps.footprint())) out[k] = (out[k] ?? 0) + n
    return out
  }
}

/**
 * Expressions over rows (BIND, FILTER), evaluated by the store for every new row of a tick in one
 * query, and cached while the row is in (so its retraction is exact).
 */
class PostCache {
  private cache = new Map<string, { out: Row[]; n: number }>()

  constructor(
    private readonly view: MaintainedView,
    private readonly post: Post | undefined,
  ) {}

  get size() {
    return this.cache.size
  }

  clear() {
    this.cache.clear()
  }

  map(delta: ZSet<Row>): ZSet<Row> {
    if (!this.post) return delta
    const fresh: [string, Row][] = []
    for (const [k, r, w] of delta.entries()) if (w > 0 && !this.cache.has(k)) fresh.push([k, r])
    if (fresh.length) {
      const results = this.view.evaluate(this.post, fresh.map(([, r]) => r))
      fresh.forEach(([k], i) => this.cache.set(k, { out: results[i], n: 0 }))
    }
    const out = new ZSet<Row>()
    for (const [k, , w] of delta.entries()) {
      const c = this.cache.get(k)
      if (!c) throw new Error(`view <${this.view.iri}>: a row left that never came in`)
      c.n += w
      for (const r of c.out) out.add(mapKey(r), r, w)
      if (c.n <= 0) this.cache.delete(k)
    }
    return out
  }

  current(z: ZSet<Row>): ZSet<Row> {
    if (!this.post) return z
    const out = new ZSet<Row>()
    for (const [k, , w] of z.entries()) {
      const c = this.cache.get(k)
      for (const r of c?.out ?? []) out.add(mapKey(r), r, w)
    }
    return out
  }
}

/** ORDER BY with LIMIT / OFFSET: every candidate row in an ordered multiset; the rows inside [offset, offset + limit) are the output. */
class TopKState {
  set: OrderedMultiset<{ row: Row; key: string }>
  /** Rows now in the window, with how many of their copies. */
  window = new Map<string, { row: Row; n: number }>()
  /** Each window row's position (1-based, first copy). */
  positions = new Map<string, number>()

  constructor(
    private spec: NonNullable<MaintainedPlan['topk']>,
    terms: Terms,
  ) {
    const order = spec.order
    this.set = new OrderedMultiset((a, b) => {
      for (const o of order) {
        const c = compareOrder(terms, a.row.get(o.slot), b.row.get(o.slot))
        if (c) return o.desc ? -c : c
      }
      return a.key < b.key ? -1 : a.key > b.key ? 1 : 0
    })
  }

  retune(spec: NonNullable<MaintainedPlan['topk']>) {
    this.spec = { ...this.spec, limit: spec.limit, offset: spec.offset }
  }

  get bounds(): [number, number] {
    const from = this.spec.offset
    return [from, this.spec.limit === undefined ? Infinity : from + this.spec.limit]
  }

  /** Apply a change of candidates; returns the change of the window. */
  apply(delta: ZSet<Row>, force = false): ZSet<Row> {
    const [, to] = this.bounds
    let reaches = force
    for (const [key, row, w] of delta.entries()) {
      const at = this.set.add({ row, key }, w)
      if (at < to) reaches = true
    }
    if (!reaches) return new ZSet()
    return this.refresh()
  }

  /** Read the window again; returns its change. */
  refresh(): ZSet<Row> {
    const [from, to] = this.bounds
    const next = new Map<string, { row: Row; n: number }>()
    const positions = new Map<string, number>()
    let pos = from
    for (const { value, n } of this.set.range(from, Math.min(to, this.set.size))) {
      next.set(value.key, { row: value.row, n })
      positions.set(value.key, pos - from + 1)
      pos += n
    }
    const out = new ZSet<Row>()
    for (const [k, e] of this.window) out.add(k, e.row, -e.n)
    for (const [k, e] of next) out.add(k, e.row, e.n)
    this.window = next
    this.positions = positions
    return out
  }

  clear() {
    this.set.clear()
    this.window.clear()
    this.positions.clear()
  }
}

/** SPARQL's ORDER BY order: unbound, blank nodes, IRIs, literals; numbers by value, dates by time, then text. */
function compareOrder(terms: Terms, a: Term | undefined, b: Term | undefined): number {
  if (!a || !b) return a ? 1 : b ? -1 : 0
  return compareTerms(terms, terms.id(a), terms.id(b))
}

export class MaintainedView implements ViewHandle {
  readonly figures: ViewFigures = { ticks: 0, rowsIn: 0, rowsOut: 0, ms: 0, lastRows: 0, lastMs: 0, partitions: 0, rebuilds: 0 }
  sampledAt?: number
  /** iv:deltaLog: the stream this view's ticks are recorded in, and the members it holds there. */
  deltaLog?: string
  private deltas: Quad[][] = []
  /** iv:whileShown: maintained only while this component (or an instance of this class) is shown. */
  whileShown?: string
  /** iv:materialize iv:Graph: a SELECT view writes its rows to the views graph (rs:). */
  materialize = false
  /** iv:materialize iv:Ephemeral: a CONSTRUCT view's triples go to subscribers only, not to the views graph. */
  ephemeral = false
  /** iv:reads: the views whose output it reads (it steps after them, and sees the views graph). */
  reads: string[] = []
  readsViews = false
  params: Param[] = []
  paramNodes: Term[] = []
  suspended = false
  private parts: PartState[] = []
  private topk?: TopKState
  private orderPost?: PostCache
  private post?: PostCache
  /** Output rows (after everything) with weights; CONSTRUCT: each row's triples. */
  private outRows = new Map<string, { row: Row; n: number; quads: Quad[] }>()
  /** Materialized SELECT: each row's node and the index written for it. */
  private written = new Map<string, { quads: Quad[]; index?: number; node: NamedNode }>()
  /** Materialized SELECT: the result set's own triples (written once it is evaluated, even with no rows). */
  private header?: Quad[]
  private recomputed?: Map<string, Quad> | Map<string, Row>
  private listeners: ((d: ViewDelta) => void)[] = []
  /** Attributed views (Definition.hosted.attribute): output rows by focus, and who hears which foci changed. */
  private byFocus = new Map<string, Set<string>>()
  private fociListeners: ((foci: Set<string>) => void)[] = []
  /** The changes of the last ticks that changed the view (since()), and the tick from which they are complete. */
  private history: { tick: number; rows: ZSet<Row>; quads: ZSet<Quad> }[] = []
  private historyKeep = 0
  private complete: number
  private readonly activity: NamedNode

  constructor(
    readonly rt: ViewRuntime,
    readonly def: Definition,
    public plan: ViewPlan,
    readonly window?: string,
  ) {
    this.activity = rt.store.factory.namedNode(mint(def.iri, 'activity'))
    this.complete = rt.tickNo
    this.build()
  }

  get iri() {
    return this.def.iri
  }

  private build() {
    const plan = this.plan
    if (plan.kind !== 'maintained') return
    this.parts = plan.parts.map((p) => new PartState(p, this))
    this.topk = plan.topk ? new TopKState(plan.topk, this.rt.terms) : undefined
    this.orderPost = plan.orderPost ? new PostCache(this, plan.orderPost) : undefined
    this.post = plan.post ? new PostCache(this, plan.post) : undefined
  }

  get strategy() {
    return this.plan.kind === 'recompute' ? ('recompute' as const) : ('incremental' as const)
  }

  get fallback() {
    return this.plan.kind === 'recompute' ? this.plan.reason : undefined
  }

  /**
   * A parameter changed: retune in place - the partitions the new constants can change are
   * evaluated again (their difference flows through the operators), and the top-k moves its
   * window to a new LIMIT / OFFSET. False when the plan's structure differs: build it again.
   */
  retune(next: ViewPlan, params: Param[], out: ZSet<Quad>, now: number, removed: readonly Quad[] = []): boolean {
    const plan = this.plan
    if (plan.kind !== 'maintained' || next.kind !== 'maintained' || next.signature !== plan.signature || !!plan.topk !== !!next.topk) return false
    const before = this.planQuads()
    this.params = params
    this.plan = next
    const happened = blank()
    const union = new ZSet<Row>()
    this.parts.forEach((ps, i) => {
      const touched = ps.retarget(next.parts[i]!, now, happened, removed)
      if (touched === 'all' || touched.size) union.plus(ps.update(touched === 'all' ? undefined : touched, now, happened))
    })
    if (this.topk) {
      this.topk.retune(next.topk!)
      out.plus(this.output(this.ordered(union, true), happened, now, false))
    } else out.plus(this.output(union, happened, now, false))
    out.plus(before, -1).plus(this.planQuads())
    return true
  }

  /** The plan as data: the view's strategy, its operators, and its PROV-O activity. */
  planQuads(): ZSet<Quad> {
    const f = this.rt.store.factory
    const nn = (p: string) => f.namedNode(p)
    const g = this.rt.graph
    const v = nn(this.iri)
    const out = new ZSet<Quad>()
    const t = (s: NamedNode, p: string, o: Term) => {
      const q = f.quad(s, nn(p), o, g)
      out.add(quadKey(q), q)
    }
    t(v, IV + 'strategy', nn(IV + (this.plan.kind === 'recompute' ? 'Recompute' : 'Incremental')))
    t(this.activity, RDF_TYPE, nn(PROV.Activity))
    t(this.activity, PROV.used, v)
    if (this.def.focus) {
      t(v, IV + 'instanceOf', nn(this.def.node))
      t(v, IV + 'focus', nn(this.def.focus))
    }
    if (this.plan.kind === 'recompute') {
      t(v, IV + 'fallback', f.literal(this.plan.reason))
      return out
    }
    const p = this.plan
    const ops: [string, string, string?][] = []
    for (const part of p.parts) {
      if (part.stream?.stream) t(this.activity, PROV.used, nn(part.stream.stream))
      ops.push(['Partition', part.algebra.local, part.keys.length ? part.keys.map((k) => '?' + k).join(' ') : '()'])
      if (part.window) ops.push(['Expire', WindowState.algebra(part.window)])
      ops.push([part.op.k === 'group' ? 'Group' : part.op.k === 'distinct' ? 'Distinct' : 'Project', part.algebra.op])
      if (part.algebra.post) ops.push(['Extend', part.algebra.post])
    }
    if (p.parts.length > 1) ops.push(['Union', `Union(${p.parts.length} parts)`])
    if (p.algebra.topk) ops.push(['TopK', p.algebra.topk])
    if (p.algebra.post) ops.push(['Extend', p.algebra.post])
    ops.push(['Sink', p.algebra.output ?? (this.materialize ? 'Rows(rs:)' : 'Rows')])
    let prev: NamedNode | undefined
    ops.forEach(([kind, algebra, key], i) => {
      const op = nn(mint(this.iri, `op-${i + 1}`))
      t(v, IV + 'operator', op)
      t(op, RDF_TYPE, nn(IV + 'Operator'))
      t(op, IV + 'kind', nn(IV + kind))
      t(op, IV + 'algebra', f.literal(algebra))
      if (key) t(op, IV + 'partitionKey', f.literal(key))
      if (prev) t(op, IV + 'input', prev)
      prev = op
    })
    return out
  }

  /** Evaluate from the current inputs (start, or after a redefinition). */
  initial(now: number): ZSet<Quad> {
    if (this.plan.kind === 'recompute') return this.recompute(true) ?? new ZSet()
    const happened = blank()
    const union = new ZSet<Row>()
    for (const ps of this.parts) union.plus(ps.all(now, happened))
    return this.output(this.ordered(union), happened, now, true)
  }

  /** One tick; undefined when the view had nothing to do. */
  step(changes: Delta, now: number): ZSet<Quad> | undefined {
    if (this.plan.kind === 'recompute') {
      if (!changes.add.length && !changes.remove.length) return undefined
      // Exact dependencies where the query's patterns could be read: only a change it can see re-runs it.
      const deps = this.plan.deps
      if (deps && ![...changes.add, ...changes.remove].some((q) => deps.route(q).length)) return undefined
      return this.recompute(false)
    }
    const t0 = performance.now()
    const happened = blank()
    const union = new ZSet<Row>()
    let any = false
    for (const ps of this.parts) {
      let touched = changes.add.length || changes.remove.length ? ps.touched(changes) : new Map<string, (Term | undefined)[]>()
      if (ps.inner.length && ps.stepDerived(changes, now, happened, touched === 'all' ? new Map() : touched) === 'all') touched = 'all'
      const win = ps.win
      if (win && (changes.add.length || changes.remove.length)) win.observe(changes)
      // A member whose timestamp moved has another bucket: its rows change.
      if (win && touched !== 'all' && ps.part.window!.bucketVar) for (const m of win.retimed) for (const k of ps.byMember.get(m) ?? []) touched.set(k, ps.partitions.get(k)!.key)
      if (touched !== 'all' && !touched.size && !win?.due(now)) continue
      any = true
      if (touched === 'all') {
        this.figures.rebuilds++
        this.rt.totals.rebuilds++
        union.plus(ps.update(undefined, now, happened))
      } else union.plus(ps.update(touched, now, happened))
    }
    if (!any) return undefined
    const out = this.output(this.ordered(union), happened, now, false)
    const ms = performance.now() - t0
    const work = happened.rowsIn + happened.groups.length + happened.rowsOut
    this.figures.ticks++
    this.figures.rowsIn += happened.rowsIn + happened.groups.length
    this.figures.rowsOut += happened.rowsOut
    this.figures.ms += ms
    this.figures.lastRows = work
    this.figures.lastMs = ms
    this.rt.totals.rowsIn += work
    if (this.deltaLog && (happened.groups.length || happened.rowsIn || happened.rowsOut)) this.logDelta(out, now, happened, work)
    return out
  }

  /** The union of the parts, through ORDER BY's keys and the top-k. */
  private ordered(union: ZSet<Row>, force = false): ZSet<Row> {
    if (!this.topk) return union
    const keyed = this.orderPost ? this.orderPost.map(union) : union
    return this.topk.apply(keyed, force)
  }

  /** Expressions after the top-k, then the output: CONSTRUCT triples, or SELECT rows. */
  private output(delta: ZSet<Row>, happened: Happened, now: number, initial: boolean): ZSet<Quad> {
    void now
    void initial
    const plan = this.plan as MaintainedPlan
    const rows = this.post ? this.post.map(delta) : delta
    const out = new ZSet<Quad>()
    const rowDelta = new ZSet<Row>()
    const f = this.rt.store.factory
    const attribute = this.def.hosted?.attribute
    const changed = new Set<string>()
    for (const [k0, r0, w] of rows.entries()) {
      // SELECT: the projected variables; CONSTRUCT: the template over every variable.
      const row = plan.form === 'select' ? project(r0, plan.vars) : r0
      const k = plan.form === 'select' ? mapKey(row) : k0
      let e = this.outRows.get(k)
      if (!e) {
        const quads = plan.form === 'construct' ? this.withProvenance(plan.output ? plan.output(row) : instantiate(f, plan.template ?? [], row, this.rt.graph)) : []
        this.outRows.set(k, (e = { row, n: 0, quads }))
      }
      e.n += w
      happened.rowsOut += Math.abs(w)
      rowDelta.add(k, row, w)
      for (const q of e.quads) out.add(quadKey(q), q, w)
      const focus = attribute ? row.get(attribute)?.value : undefined
      if (focus !== undefined) {
        changed.add(focus)
        let keys = this.byFocus.get(focus)
        if (e.n > 0) {
          if (!keys) this.byFocus.set(focus, (keys = new Set()))
          keys.add(k)
        } else if (keys) {
          keys.delete(k)
          if (!keys.size) this.byFocus.delete(focus)
        }
      }
      if (e.n <= 0) this.outRows.delete(k)
    }
    if (changed.size) for (const l of this.fociListeners) l(changed)
    if (plan.form === 'select' && this.materialize) this.writeRows(out)
    this.notify(rowDelta, out)
    return this.ephemeral ? new ZSet() : out
  }

  /**
   * A materialized SELECT's rows in the views graph, in the W3C result-set vocabulary:
   *   <view> a rs:ResultSet ; rs:resultVariable "v"… ; rs:solution <row> .
   *   <row> rs:binding <row>-v ; rs:index n (ORDER BY only) .   <row>-v rs:variable "v" ; rs:value ?value .
   * A row's node is keyed by its values (stable while they are), minted under the view - or,
   * for an instance of a template, under its focus, so rules on the focus can read it.
   */
  private writeRows(out: ZSet<Quad>) {
    const plan = this.plan as MaintainedPlan
    const f = this.rt.store.factory
    const nn = (x: string) => f.namedNode(x)
    const g = this.rt.graph
    const base = nn(this.iri)
    const int = (n: number) => f.literal(String(n), nn(NS.xsd + 'integer'))
    if (!this.header) {
      this.header = [f.quad(base, nn(RDF_TYPE), nn(RS + 'ResultSet'), g), ...plan.vars.map((v) => f.quad(base, nn(RS + 'resultVariable'), f.literal(v), g))]
      for (const q of this.header) out.add(quadKey(q), q, 1)
    }
    const seen = new Set<string>()
    for (const [k, e] of this.outRows) {
      seen.add(k)
      const index = this.topk ? this.topkIndex(e.row) : undefined
      const had = this.written.get(k)
      if (had && had.index === index) continue
      if (had) {
        if (had.index !== undefined) out.add(quadKey(f.quad(had.node, nn(RS + 'index'), int(had.index), g)), f.quad(had.node, nn(RS + 'index'), int(had.index), g), -1)
        if (index !== undefined) out.add(quadKey(f.quad(had.node, nn(RS + 'index'), int(index), g)), f.quad(had.node, nn(RS + 'index'), int(index), g), 1)
        had.index = index
        continue
      }
      const node = nn(this.iri + (this.iri.includes('/~') ? '-' : '/~row-') + md5(k).slice(0, 12))
      const quads = [f.quad(base, nn(RS + 'solution'), node, g)]
      for (const [v, t] of e.row) {
        const b = nn(node.value + '-' + v)
        quads.push(f.quad(node, nn(RS + 'binding'), b, g), f.quad(b, nn(RS + 'variable'), f.literal(v), g), f.quad(b, nn(RS + 'value'), t, g))
      }
      if (e.n > 1) quads.push(f.quad(node, nn(IV + 'multiplicity'), int(e.n), g))
      for (const q of quads) out.add(quadKey(q), q, 1)
      if (index !== undefined) out.add(quadKey(f.quad(node, nn(RS + 'index'), int(index), g)), f.quad(node, nn(RS + 'index'), int(index), g), 1)
      this.written.set(k, { quads, index, node })
    }
    for (const [k, w] of [...this.written]) {
      if (seen.has(k)) continue
      for (const q of w.quads) out.add(quadKey(q), q, -1)
      if (w.index !== undefined) out.add(quadKey(f.quad(w.node, nn(RS + 'index'), int(w.index), g)), f.quad(w.node, nn(RS + 'index'), int(w.index), g), -1)
      this.written.delete(k)
    }
  }

  private topkIndex(row: Row): number | undefined {
    if (!this.topk) return undefined
    for (const [k, e] of this.topk.window) if (mapKey(project(e.row, (this.plan as MaintainedPlan).vars)) === mapKey(row)) return this.topk.positions.get(k)
    return undefined
  }

  index(row: Row): number | undefined {
    return this.topkIndex(row)
  }

  /** Evaluate expressions (BIND, FILTER) over rows with the store: one query for all of them. */
  evaluate(post: Post, rows: Row[]): Row[][] {
    const vars = post.inputs
    // Blank nodes (VALUES cannot hold them) go in as IRIs of their own and come back.
    const blanks = new Map<string, Term>()
    const term = (t: Term | undefined) => {
      if (t?.termType !== 'BlankNode') return sparqlTerm(t)
      const iri = `urn:semblance:blank:${encodeURIComponent(t.value)}`
      blanks.set(iri, t)
      return `<${iri}>`
    }
    const values = rows.map((r, i) => `(${i} ${vars.map((v) => term(r.get(v))).join(' ')})`).join(' ')
    const query = `${post.prologue ?? ''}SELECT * WHERE {\n  VALUES (?__i ${vars.map((v) => '?' + v).join(' ')}) { ${values} }\n${post.values ? `  ${post.values}\n` : ''}${post.binds.map((b) => `  BIND(${b.expr} AS ?${b.as})`).join('\n')}\n${post.filters.map((x) => `  FILTER(${x})`).join('\n')}\n}`
    const out: Row[][] = rows.map(() => [])
    for (const r of this.rt.runner(this.readsViews).select(query)) {
      const i = Number(r.get('__i')!.value)
      r.delete('__i')
      if (blanks.size) for (const [k, t] of r) if (t.termType === 'NamedNode' && blanks.has(t.value)) r.set(k, blanks.get(t.value)!)
      out[i]!.push(r)
    }
    return out
  }

  /**
   * iv:deltaLog: one member per tick that changed the view - what came in (partitions appended,
   * retired, changed, aged out of the window), which groups changed and how, and the work it
   * took against what a re-run reads - so a page can show each step. Keeps the newest few
   * (the log's ldes:amount, default 12).
   */
  private logDelta(out: ZSet<Quad>, now: number, happened: Happened, work: number) {
    const f = this.rt.store.factory
    const nn = (x: string) => f.namedNode(x)
    const g = this.rt.graph
    const log = nn(this.deltaLog!)
    const tick = nn(mint(this.deltaLog!, `${encodeURIComponent(localName(this.iri))}-${this.rt.tickNo}`))
    const int = (n: number) => f.literal(String(n), nn(NS.xsd + 'integer'))
    const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`
    const came = [
      happened.appended && `+${plural(happened.appended, 'member')}`,
      happened.retired && `−${plural(happened.retired, 'member')} retired`,
      happened.expired && `−${plural(happened.expired, 'member')} aged out`,
      happened.changed && `${plural(happened.changed, 'member')} changed`,
    ].filter(Boolean)
    const changes = happened.groups
    const shown = changes.slice(0, 3).map((c) => `${c.key}: ${c.before?.value ?? '–'} → ${c.after?.value ?? '–'}`)
    if (changes.length > 3) shown.push(`${changes.length - 3} more`)
    if (!changes.length && happened.rowsOut) shown.push(`${plural(happened.rowsOut, 'row')} out`)
    const quads = [
      f.quad(log, nn(NS.tree + 'member'), tick, g),
      f.quad(tick, nn(RDF_TYPE), nn(IV + 'Delta'), g),
      f.quad(tick, nn(IV + 'view'), nn(this.iri), g),
      f.quad(tick, nn(IV + 'tick'), int(this.rt.tickNo), g),
      f.quad(tick, nn(IV + 'at'), f.literal(new Date(now).toISOString(), nn(NS.xsd + 'dateTime')), g),
      f.quad(tick, nn(IV + 'came'), f.literal(came.join(', ') || 'nothing came in'), g),
      f.quad(tick, nn(IV + 'changed'), f.literal(shown.join('; ') || 'no group changed'), g),
      f.quad(tick, nn(IV + 'entered'), int(happened.appended), g),
      f.quad(tick, nn(IV + 'left'), int(happened.retired + happened.expired), g),
      f.quad(tick, nn(IV + 'work'), int(work), g),
      f.quad(tick, nn(IV + 'inputRows'), int(this.inputRows()), g),
    ]
    // With iv:recordsChanges on the log, the tick's triples themselves: what came and went, as
    // rdf:Statement nodes, so a reader can catch up from a tick number with SPARQL alone.
    if (this.rt.store.match(log, nn(IV + 'recordsChanges'), null).some((q) => q.object.value === 'true')) {
      let i = 0
      for (const [, q, w] of [...out.entries()]) {
        const st = nn(`${tick.value}-${w > 0 ? 'a' : 'r'}${i++}`)
        quads.push(
          f.quad(tick, nn(IV + (w > 0 ? 'added' : 'removed')), st, g),
          f.quad(st, nn(RDF_TYPE), nn(NS.rdf + 'Statement'), g),
          f.quad(st, nn(NS.rdf + 'subject'), q.subject as NamedNode, g),
          f.quad(st, nn(NS.rdf + 'predicate'), q.predicate as NamedNode, g),
          f.quad(st, nn(NS.rdf + 'object'), q.object as NamedNode, g),
        )
      }
    }
    for (const q of quads) out.add(quadKey(q), q, 1)
    this.deltas.push(quads)
    const keep = this.rt.retained(this.deltaLog!) ?? 12
    while (this.deltas.length > keep) for (const q of this.deltas.shift()!) out.add(quadKey(q), q, -1)
  }

  /** Subjects the view types were generated by its activity; others it decorates were influenced. */
  private withProvenance(quads: Quad[]): Quad[] {
    // A hosted view's output is someone else's data (what a policy stores), not the views graph's.
    if (this.def.hosted) return quads
    const f = this.rt.store.factory
    const typed = new Set(quads.filter((q) => q.predicate.value === RDF_TYPE).map((q) => q.subject.value))
    const subjects = new Map(quads.map((q) => [q.subject.value, q.subject]))
    const out = [...quads]
    for (const [s, node] of subjects) out.push(f.quad(node, f.namedNode(typed.has(s) ? PROV.wasGeneratedBy : PROV.wasInfluencedBy), this.activity, this.rt.graph))
    return out
  }

  private recompute(initial: boolean): ZSet<Quad> | undefined {
    const p = this.plan as Extract<ViewPlan, { kind: 'recompute' }>
    const t0 = performance.now()
    const run = this.rt.runner(this.readsViews)
    const out = new ZSet<Quad>()
    const rowDelta = new ZSet<Row>()
    if (p.form === 'construct') {
      const next = new Map(this.withProvenance(run.construct(p.query)).map((q) => [quadKey(q), q]))
      const prev = (this.recomputed as Map<string, Quad> | undefined) ?? new Map()
      for (const [k, q] of prev) if (!next.has(k)) out.add(k, q, -1)
      for (const [k, q] of next) if (!prev.has(k)) out.add(k, q, 1)
      this.recomputed = next
      if (this.ephemeral) {
        this.notify(rowDelta, out)
        return new ZSet()
      }
    } else {
      const next = new Map<string, Row>()
      const counts = new ZSet<Row>()
      for (const r of run.select(p.query)) counts.add(mapKey(r), r, 1)
      const prev = (this.recomputed as Map<string, Row> | undefined) ?? new Map()
      const prevCounts = new ZSet<Row>()
      for (const [k, r] of prev) prevCounts.add(k.slice(k.indexOf('|') + 1), r, 1)
      rowDelta.plus(counts).plus(prevCounts, -1)
      for (const [k, r, w] of counts.entries()) for (let i = 0; i < w; i++) next.set(i + '|' + k, r)
      this.recomputed = next
    }
    const ms = performance.now() - t0
    if (!initial) {
      this.figures.ticks++
      this.figures.ms += ms
      this.figures.lastMs = ms
    }
    this.notify(rowDelta, out)
    return out
  }

  /** How many of the view's solutions give each output triple: what is a triple of the output (a set). */
  private quadCounts = new Map<string, number>()

  private notify(rows: ZSet<Row>, weighted: ZSet<Quad>, set = false) {
    // Readers get the output's change as a set: a triple comes when its first solution does, goes with its last.
    const quads = set ? weighted : new ZSet<Quad>()
    if (!set)
      for (const [k, q, w] of weighted.entries()) {
        const before = this.quadCounts.get(k) ?? 0
        const after = before + w
        if (after > 0) this.quadCounts.set(k, after)
        else this.quadCounts.delete(k)
        if (before <= 0 && after > 0) quads.add(k, q, 1)
        else if (before > 0 && after <= 0) quads.add(k, q, -1)
      }
    if (!rows.size && !quads.size) return
    if (this.historyKeep) {
      const tick = this.rt.tickNo
      let last = this.history[this.history.length - 1]
      if (last?.tick !== tick) this.history.push((last = { tick, rows: new ZSet(), quads: new ZSet() }))
      last.rows.plus(rows)
      last.quads.plus(quads)
      while (this.history.length > this.historyKeep) this.complete = this.history.shift()!.tick
    } else this.complete = this.rt.tickNo
    for (const l of this.listeners) l(this.plan.form === 'select' ? { rows } : { quads })
  }

  /** An attributed view's output for one focus (its rows' triples, once each). */
  quadsFor(focus: string): Quad[] {
    const seen = new Map<string, Quad>()
    for (const k of this.byFocus.get(focus) ?? []) for (const q of this.outRows.get(k)?.quads ?? []) seen.set(quadKey(q), q)
    return [...seen.values()]
  }

  /** Hear which foci an attributed view's output changed for. */
  onFoci(fn: (foci: Set<string>) => void): () => void {
    this.fociListeners.push(fn)
    return () => (this.fociListeners = this.fociListeners.filter((x) => x !== fn))
  }

  keepHistory(ticks: number): void {
    this.historyKeep = Math.max(this.historyKeep, ticks)
  }

  since(tick: number): ViewChange | undefined {
    if (tick < this.complete) return undefined
    const out: ViewChange = { tick: this.rt.tickNo, rows: new ZSet(), quads: new ZSet() }
    for (const h of this.history) {
      if (h.tick <= tick) continue
      out.rows.plus(h.rows)
      out.quads.plus(h.quads)
    }
    return out
  }

  /** The current output as one change (a reader starting from nothing). */
  snapshot(): ViewChange {
    const out: ViewChange = { tick: this.rt.tickNo, rows: new ZSet(), quads: new ZSet() }
    for (const r of this.rows()) out.rows.add(mapKey(r), r, 1)
    for (const q of this.outputQuads()) out.quads.add(quadKey(q), q, 1)
    return out
  }

  /** Every triple the view's output holds now (a materialized SELECT's rs: rows too). */
  outputQuads(): Quad[] {
    if (this.plan.form === 'construct') return this.quads()
    const out: Quad[] = [...(this.header ?? [])]
    const f = this.rt.store.factory
    for (const w of this.written.values()) {
      out.push(...w.quads)
      if (w.index !== undefined) out.push(f.quad(w.node, f.namedNode(RS + 'index'), f.literal(String(w.index), f.namedNode(NS.xsd + 'integer')), this.rt.graph))
    }
    return out
  }

  /** What leaves when the view is emptied (a rebuild, a redefinition): the current output, negated. */
  private retractAll() {
    const now = this.snapshot()
    this.quadCounts.clear()
    this.notify(now.rows.negate(), now.quads.negate(), true)
  }

  /** Evaluate again from the inputs (after a suspension): the change is the difference. */
  rebuild(now: number): ZSet<Quad> {
    const out = new ZSet<Quad>()
    this.retractAll()
    this.reset(out)
    this.recomputed = undefined
    return out.plus(this.initial(now))
  }

  /** Retract everything (the view is redefined or removed). */
  drop(out: ZSet<Quad>) {
    this.retractAll()
    out.plus(this.planQuads(), -1)
    for (const quads of this.deltas) for (const q of quads) out.add(quadKey(q), q, -1)
    this.deltas = []
    this.reset(out)
  }

  private reset(out: ZSet<Quad>) {
    if (!this.ephemeral) for (const q of this.quads()) out.add(quadKey(q), q, -1)
    if (this.plan.kind === 'maintained' && this.plan.form === 'select') {
      const f = this.rt.store.factory
      for (const w of this.written.values()) {
        for (const q of w.quads) out.add(quadKey(q), q, -1)
        if (w.index !== undefined) {
          const q = f.quad(w.node, f.namedNode(RS + 'index'), f.literal(String(w.index), f.namedNode(NS.xsd + 'integer')), this.rt.graph)
          out.add(quadKey(q), q, -1)
        }
      }
      for (const q of this.header ?? []) out.add(quadKey(q), q, -1)
      this.header = undefined
    }
    this.written.clear()
    // An attributed view's foci all lose their output (the engine hears it).
    if (this.byFocus.size) {
      const foci = new Set(this.byFocus.keys())
      this.byFocus.clear()
      for (const l of this.fociListeners) l(foci)
    }
    for (const ps of this.parts) ps.reset()
    this.topk?.clear()
    this.orderPost?.clear()
    this.post?.clear()
    this.outRows.clear()
  }

  rows(): Row[] {
    if (this.plan.kind === 'recompute') return this.plan.form === 'select' ? [...((this.recomputed as Map<string, Row>) ?? new Map()).values()] : []
    const out: Row[] = []
    for (const e of this.outRows.values()) for (let i = 0; i < e.n; i++) out.push(e.row)
    return out
  }

  quads(): Quad[] {
    if (this.plan.kind === 'recompute') return this.plan.form === 'construct' ? [...((this.recomputed as Map<string, Quad>) ?? new Map()).values()] : []
    const seen = new Map<string, Quad>()
    for (const e of this.outRows.values()) for (const q of e.quads) seen.set(quadKey(q), q)
    return [...seen.values()]
  }

  subscribe(fn: (d: ViewDelta) => void, opts: { replay?: boolean } = {}): () => void {
    this.listeners.push(fn)
    if (opts.replay) {
      const rows = new ZSet<Row>()
      for (const r of this.rows()) rows.add(mapKey(r), r, 1)
      const quads = new ZSet<Quad>()
      for (const q of this.quads()) quads.add(quadKey(q), q, 1)
      fn(this.plan.form === 'select' ? { rows } : { quads })
    }
    return () => (this.listeners = this.listeners.filter((l) => l !== fn))
  }

  footprint(): Record<string, number> {
    const out: Record<string, number> = { recomputed: this.recomputed?.size ?? 0, topk: this.topk?.set.count ?? 0, out: this.outRows.size, written: this.written.size }
    for (const ps of this.parts) for (const [k, n] of Object.entries(ps.footprint())) out[k] = (out[k] ?? 0) + n
    return out
  }

  /** Rows of state: partition rows, groups, counts, window entries, cached expressions, top-k candidates (or a recomputed result). */
  stateRows(): number {
    let n = (this.recomputed?.size ?? 0) + (this.topk?.set.count ?? 0) + (this.orderPost?.size ?? 0) + (this.post?.size ?? 0)
    for (const ps of this.parts) n += ps.stateRows()
    return n
  }

  /** Event-time windows: members that arrived behind the watermark (undefined without one). */
  lateMembers(): number | undefined {
    const wins = this.parts.flatMap((ps) => (ps.win?.spec.eventTime ? [ps.win] : []))
    return wins.length ? wins.reduce((n, w) => n + w.late, 0) : undefined
  }

  /** Rows a from-scratch evaluation reads: the partitions' rows (for the figures). */
  inputRows(): number {
    let n = 0
    for (const ps of this.parts) n += ps.partitionRows
    return n
  }

  nextExpiry(): number | undefined {
    let next: number | undefined
    for (const ps of this.parts) {
      const at = ps.win?.nextExpiry()
      if (at !== undefined && (next === undefined || at < next)) next = at
    }
    return next
  }

  /** Evaluate the view's query from scratch (the oracle). */
  fromScratch(now: number): (Quad | Row)[] {
    const run = this.rt.runner(this.readsViews)
    const query = this.plan.kind === 'recompute' ? this.plan.query : this.plan.reference?.(now)
    if (!query) return []
    return this.plan.form === 'construct' ? run.construct(query) : run.select(query)
  }

  /** undefined when the maintained result equals the from-scratch one; else what differs. */
  compare(now: number): string | undefined {
    const query = this.plan.kind === 'recompute' ? this.plan.query : this.plan.reference?.(now)
    if (!query) return undefined
    const expected = new ZSet<string>()
    const actual = new ZSet<string>()
    const unordered = this.plan.kind === 'maintained' ? this.plan.unordered : undefined
    const normQuad = (q: Quad) => normQuadIn(q, unordered)
    const normRow = (r: Row) => normRowIn(r, unordered)
    if (this.plan.form === 'construct') {
      // CONSTRUCT results are sets; provenance is the view's own decoration.
      const isProv = (q: Quad) => q.predicate.value === PROV.wasGeneratedBy || q.predicate.value === PROV.wasInfluencedBy
      for (const k of new Set((this.fromScratch(now) as Quad[]).filter((q) => !isProv(q)).map(normQuad))) expected.add(k, '', 1)
      for (const k of new Set(this.quads().filter((q) => !isProv(q)).map(normQuad))) actual.add(k, '', 1)
    } else {
      const scratch = this.fromScratch(now) as Row[]
      const reduced = this.plan.kind === 'maintained' && this.plan.reduced
      for (const r of reduced ? [...new Map(scratch.map((x) => [normRow(x), x])).values()] : scratch) expected.add(normRow(r), '', 1)
      for (const r of this.rows()) actual.add(normRow(r), '', 1)
      // Ordered views: positions too, where the order is total (no ties among the expected rows).
      const plan = this.plan
      if (plan.kind === 'maintained' && plan.topk && !plan.orderPost && plan.topk.order.every((o) => plan.vars.includes(o.slot))) {
        const orderKey = (r: Row) => plan.topk!.order.map((o) => (r.get(o.slot) ? termKey(r.get(o.slot)!) : '')).join(' ')
        const counts = new Map<string, number>()
        for (const r of scratch) counts.set(orderKey(r), (counts.get(orderKey(r)) ?? 0) + 1)
        for (let i = 0; i < scratch.length; i++) {
          if (counts.get(orderKey(scratch[i]!))! > 1) continue
          const at = this.index(project(scratch[i]!, plan.vars))
          if (at !== undefined && at !== i + 1) return `row ${normRow(scratch[i]!)} is at ${at}, from scratch at ${i + 1}`
        }
      }
    }
    const diff = new ZSet<string>().plus(actual).plus(expected, -1)
    if (!diff.size) return undefined
    return [...diff.entries()].slice(0, 6).map(([k, , w]) => `${w > 0 ? 'extra' : 'missing'} ${k}`).join('; ')
  }
}

const blank = (): Happened => ({ appended: 0, retired: 0, changed: 0, expired: 0, groups: [], rowsIn: 0, rowsOut: 0 })

/** A row with only the given variables. */
function project(r: Row, vars: string[]): Row {
  const out: Row = new Map()
  for (const v of vars) {
    const t = r.get(v)
    if (t) out.set(v, t)
  }
  return out
}

/** A CONSTRUCT template over one row: triples with an unbound (or ill-placed) term are dropped. */
function instantiate(f: import('../store/types.ts').Factory, template: Triple[], row: Row, g: Term): Quad[] {
  const term = (n: Node | Triple['p']): Term | undefined => {
    switch (n.k) {
      case 'var':
        return row.get(n.v)
      case 'iri':
        return f.namedNode(n.v)
      case 'link':
        return f.namedNode(n.iri)
      case 'lit':
        return n.lang ? f.literal(n.value, n.lang) : f.literal(n.value, n.dt ? f.namedNode(n.dt) : undefined)
      default:
        return undefined
    }
  }
  const out: Quad[] = []
  for (const t of template) {
    const s = term(t.s)
    const p = term(t.p)
    const o = term(t.o)
    if (!s || !p || !o || s.termType === 'Literal' || p.termType !== 'NamedNode') continue
    out.push(f.quad(s as NamedNode, p, o, g as NamedNode))
  }
  return out
}

/** A group's key, readably: local names of IRIs, values of literals. */
function groupLabel(terms: (Term | undefined)[]): string {
  return terms.map((t) => (!t ? '–' : t.termType === 'NamedNode' ? localName(t.value) : t.value)).join(' · ')
}

function sameRows(a: number[][], b: number[][]): boolean {
  if (a.length !== b.length) return false
  const ka = a.map(rowKey).sort()
  const kb = b.map(rowKey).sort()
  return ka.every((k, i) => k === kb[i])
}

export const mapKey = (row: Row) =>
  [...row.entries()]
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([k, t]) => `${k}=${termKey(t)}`)
    .join(' ')

/** Numeric literals compare by value, to 12 digits (lexical forms and float sums differ between engines). */
function norm(t: Term, unordered?: string): string {
  if (t.termType === 'Literal' && /#(integer|decimal|double|float|int|long)$/.test(t.datatype.value) && !Number.isNaN(Number(t.value))) return `${Number(Number(t.value).toPrecision(12))}^^numeric`
  // A GROUP_CONCAT: its parts, in any order.
  if (unordered !== undefined && t.termType === 'Literal') return (t.language ? '@' + t.language : '^^' + t.datatype.value) + ' parts:' + (unordered ? t.value.split(unordered) : [...t.value]).sort().join('\u0000')
  return termKey(t)
}
const normQuadIn = (q: Quad, unordered?: Map<string, string>) => `${norm(q.subject)} ${q.predicate.value} ${norm(q.object, unordered?.get(q.predicate.value))}`
const normRowIn = (r: Row, unordered?: Map<string, string>) =>
  [...r.entries()]
    .filter(([k]) => !k.startsWith('__'))
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(([k, t]) => `${k}=${norm(t, unordered?.get(k))}`)
    .join(' ')
