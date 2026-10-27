import type { Quad } from '@rdfjs/types'
import { type Delta, mint, quadKey } from './rdf.ts'
import { Engine, type EngineOptions } from './engine/engine.ts'
import { functions as builtinFunctions } from './components/functions.ts'
import { HtmlIndex } from './render/html-index.ts'
import type { GraphStore } from './store/types.ts'
import { EVENTS_GRAPH, NAV_GRAPH, NETWORK_GRAPH, PERF_GRAPH, PERSIST_GRAPH, SEM } from './ns.ts'
import { docsFunctions } from './layers/docs.ts'
import { persistenceFunctions } from './persistence.ts'
import { navigationFunctions } from './navigation.ts'
import { privacyFunctions } from './layers/privacy.ts'
import { functions as workbenchFunctions } from './components/workbench.ts'
import { exampleFunctions } from './examples/index.ts'
import { functions as referenceFunctions } from './components/reference.ts'
import { VIEW_GRAPH, ViewRuntime } from './view/runtime.ts'
import { viewFunctions } from './view/functions.ts'
import { Watchdog } from './engine/watchdog.ts'

/** An engine plus the RDF/HTML view it keeps up to date. */
export interface Projection {
  engine: Engine
  index: HtmlIndex
}

/**
 * One graph, many projections. The runtime owns the store; every projection
 * (one per modality or view) derives its own graph from the same input, and
 * every input change is applied once and propagated to all.
 */
export class Runtime {
  readonly projections: Projection[] = []
  private readonly inputListeners: ((graph: string | undefined, delta: Delta) => void)[] = []
  private readonly warmth = new WeakMap<Engine, (focus: string) => string | undefined>()
  private readonly refreshListeners: (() => void)[] = []
  private refreshQueued = false
  /** When the next refresh may run: a slow one waits longer, so following never takes over. */
  private refreshAfter = 0

  /**
   * How a refresh waits for the next frame (see refresh()): requestAnimationFrame where there
   * is a page, else a microtask. Hosts and tests may replace it.
   */
  frame: (run: () => void) => void =
    typeof requestAnimationFrame === 'function' ? (run) => void requestAnimationFrame(() => run()) : (run) => queueMicrotask(run)
  /**
   * Following a query never takes more than about this share of the time: after a refresh
   * that took t ms, the next waits at least t / share - t ms more.
   */
  refreshShare = 0.25

  /** Maintained views (docs/IVM.md): stepped once per tick, before the projections. */
  readonly views: ViewRuntime
  /**
   * The turn budget every projection and view of this runtime shares (src/engine/watchdog.ts): a
   * turn over it is suspended, its pending work held until continueStall() or stopStall().
   */
  readonly watchdog = new Watchdog()
  /** The open transaction: every effective change since it began, by graph and quad (net weight). */
  private tx?: Map<string, { graph: string | undefined; q: Quad; w: number }>

  constructor(readonly store: GraphStore) {
    this.views = new ViewRuntime(store, { graphs: [EVENTS_GRAPH, PERF_GRAPH, PERSIST_GRAPH, NETWORK_GRAPH, NAV_GRAPH], functions: viewFunctions })
    // A window's next expiry: a tick with no input.
    this.views.onTimer = () => {
      this.advance()
      for (const l of this.timerListeners) l()
    }
    // A view maintained while something is shown asks the projections that show it; what the
    // person last showed intent to see (preload) counts as shown, so its views are ready by the click.
    this.views.shown = (component) => this.projections.some((p) => !p.engine.demand || p.engine.demand(component) || this.intended(p, component))
    this.views.watchdog = this.watchdog
  }

  private readonly timerListeners: (() => void)[] = []

  /** Hear about ticks time alone caused (a view's window moved), e.g. to patch the DOM. */
  onTimer(listener: () => void): () => void {
    this.timerListeners.push(listener)
    return () => this.timerListeners.splice(this.timerListeners.indexOf(listener), 1)
  }

  /**
   * Run `fn` as one tick: the changes it applies or writes (to any graph) reach views,
   * projections and input listeners once, as one consolidated change, when it returns.
   * The store itself changes at once, so `fn` reads its own writes. Nested calls join
   * the outer transaction.
   */
  transaction<T>(fn: () => T): T {
    if (this.tx) return fn()
    this.tx = new Map()
    try {
      return fn()
    } finally {
      const tx = this.tx
      this.tx = undefined
      this.flush(tx)
    }
  }

  private disposed = false

  /**
   * Let the runtime and its store go: no timer or queued refresh touches the store afterwards,
   * and the store releases what it holds outside the JavaScript heap (GraphStore.dispose).
   */
  dispose(): void {
    if (this.disposed) return
    this.disposed = true
    this.views.dispose()
    this.store.dispose?.()
  }

  /** A tick with no input change: time passed (windows of maintained views expire). */
  advance(): void {
    this.tick(new Map(), { add: [], remove: [] })
  }

  private changed(graph: string | undefined, effective: Delta) {
    if (!effective.add.length && !effective.remove.length) return
    // Outside a transaction a change is its own tick, exactly as applied.
    if (!this.tx) return this.tick(new Map([[graph, effective]]), effective)
    for (const [w, qs] of [[-1, effective.remove], [1, effective.add]] as const) {
      for (const q of qs) {
        const k = (graph ?? '') + ' ' + quadKey(q)
        const e = this.tx.get(k)
        if (e) e.w += w
        else this.tx.set(k, { graph, q, w })
      }
    }
  }

  /** A transaction's changes, consolidated: a triple added and removed again is no change. */
  private flush(tx: Map<string, { graph: string | undefined; q: Quad; w: number }>) {
    const all: Delta = { add: [], remove: [] }
    const byGraph = new Map<string | undefined, Delta>()
    for (const { graph, q, w } of tx.values()) {
      if (!w) continue
      let d = byGraph.get(graph)
      if (!d) byGraph.set(graph, (d = { add: [], remove: [] }))
      ;(w > 0 ? d.add : d.remove).push(q)
      ;(w > 0 ? all.add : all.remove).push(q)
    }
    if (all.add.length || all.remove.length) this.tick(byGraph, all)
  }

  /** One tick: views first, so the projections see their outputs in the same tick; then projections; then listeners. */
  private tick(byGraph: Map<string | undefined, Delta>, input: Delta) {
    this.watchdog.turn(() => this.tickWatched(byGraph, input))
  }

  private tickWatched(byGraph: Map<string | undefined, Delta>, input: Delta) {
    if (this.disposed) return
    const views = this.views.tick(input)
    const all = views.add.length || views.remove.length ? { add: [...input.add, ...views.add], remove: [...input.remove, ...views.remove] } : input
    if (!all.add.length && !all.remove.length) return
    for (const p of this.projections) p.engine.process(all), settle(p)
    this.resumeViews()
    for (const [graph, d] of byGraph) for (const l of this.inputListeners) l(graph, d)
  }

  /** Views suspended while their components were hidden, shown now: rebuilt, and projected. */
  private resumeViews(): boolean {
    const resumed = this.views.resume()
    if (!resumed.add.length && !resumed.remove.length) return false
    for (const p of this.projections) p.engine.process(resumed), settle(p)
    return true
  }

  /** Hear about every refresh (after the projections are updated): the host flushes its renderers. */
  onRefresh(listener: () => void): () => void {
    this.refreshListeners.push(listener)
    return () => this.refreshListeners.splice(this.refreshListeners.indexOf(listener), 1)
  }

  /**
   * Re-run the views whose followed queries read what changed since the last refresh (see
   * Engine.refresh), in every projection. The host calls it at the next frame by itself
   * (`frame`); call it to refresh now. Returns whether anything was re-run.
   */
  refresh(): boolean {
    return this.watchdog.turn(() => this.refreshWatched())
  }

  private refreshWatched(): boolean {
    if (this.disposed) return false
    const t = performance.now()
    let any = false
    for (const p of this.projections) if (p.engine.refresh()) settle(p), (any = true)
    if (!any) return false
    const took = performance.now() - t
    this.refreshAfter = performance.now() + took * (1 / this.refreshShare - 1)
    for (const l of this.refreshListeners) l()
    return true
  }

  /** At most one refresh waits at a time: everything that changes before it runs is in it. */
  private queueRefresh() {
    if (this.refreshQueued) return
    this.refreshQueued = true
    const go = () => {
      const wait = this.refreshAfter - performance.now()
      if (wait > 1) return void setTimeout(() => this.frame(go), wait)
      this.refreshQueued = false
      this.refresh()
    }
    this.frame(go)
  }

  /**
   * Hear about every effective change to the input graphs (`graph` undefined = the
   * default graph): host effects such as persistence follow the input this way.
   */
  onInput(listener: (graph: string | undefined, delta: Delta) => void): () => void {
    this.inputListeners.push(listener)
    return () => this.inputListeners.splice(this.inputListeners.indexOf(listener), 1)
  }

  /**
   * `roots` (optional) are the instances this projection is mounted at: only
   * what is shown under them is evaluated (see Engine.demand); the rest waits.
   */
  project(opts: EngineOptions & { roots?: string[] }, onError?: (e: Error) => void): Projection {
    // Views start with the first projection (whose rule preference they share), before it reads the store.
    if (!this.views.isStarted) this.views.start(opts.prefer)
    return this.watchdog.turn(() => this.projectWatched(opts, onError))
  }

  private projectWatched(opts: EngineOptions & { roots?: string[] }, onError?: (e: Error) => void): Projection {
    const name = opts.name ?? 'main'
    const engine = new Engine(this.store, {
      inputGraphs: [EVENTS_GRAPH, PERF_GRAPH, PERSIST_GRAPH, NETWORK_GRAPH, NAV_GRAPH, VIEW_GRAPH],
      // A rule maintained as a view (iv:strategy iv:Incremental): one hosted view per rule and projection.
      maintain: (rule, query) => this.views.hostRule(mint(rule.iri, 'view-' + name), query),
      ...opts,
      functions: { ...builtinFunctions, ...docsFunctions, ...persistenceFunctions, ...navigationFunctions, ...workbenchFunctions, ...referenceFunctions, ...privacyFunctions, ...exampleFunctions, ...opts.functions },
    })
    const index = new HtmlIndex(onError)
    engine.watchdog = this.watchdog
    engine.onChange((d) => index.apply(d))
    engine.onStale = () => this.queueRefresh()
    if (opts.roots) {
      const roots = new Set(opts.roots)
      // Whether a focus is shown is asked for every parked pair on every check; the answer holds
      // until something that decides visibility changes (index.visibility), so it is kept until then.
      let epoch = -1
      let shown = new Map<string, boolean>()
      engine.demand = (focus) => {
        if (epoch !== index.visibility) (epoch = index.visibility), (shown = new Map())
        let v = shown.get(focus)
        if (v === undefined) shown.set(focus, (v = index.isShown(focus, roots)))
        return v
      }
      // Idle time warms only what an ancestor declares sem:warm sem:Eager (default: lazy).
      const f = this.store.factory
      const warm = f.namedNode(SEM.warm)
      const warmth = (focus: string) => index.nearest(focus, (key) => (key.includes(' ') ? undefined : this.store.match(f.namedNode(key), warm, null)[0]?.object.value))
      engine.warmable = (focus) => warmth(focus) === SEM.Eager
      this.warmth.set(engine, warmth)
    }
    engine.start()
    const p: Projection = { engine, index }
    settle(p)
    this.projections.push(p)
    this.resumeViews()
    return p
  }

  /**
   * Change a named input graph (the change log, performance observations...)
   * and update the projections that read it.
   */
  write(graph: string, delta: Delta): Delta {
    const f = this.store.factory
    const g = f.namedNode(graph)
    const effective: Delta = { add: [], remove: [] }
    for (const q of delta.remove) {
      const e = f.quad(q.subject, q.predicate, q.object, g)
      if (this.store.has(e)) this.store.delete(e), effective.remove.push(e)
    }
    for (const q of delta.add) {
      const e = f.quad(q.subject, q.predicate, q.object, g)
      if (!this.store.has(e)) this.store.add(e), effective.add.push(e)
    }
    this.changed(graph, effective)
    return effective
  }

  /** Append to (or retire from) the change log. Callers write complete, immutable members. */
  log(delta: Delta): void {
    this.write(EVENTS_GRAPH, delta)
  }

  /** Spend up to `budgetMs` evaluating hidden work (see Engine.warm). Returns whether work remains. */
  warm(budgetMs: number): boolean {
    const until = performance.now() + budgetMs
    let more = false
    const share = budgetMs / Math.max(1, this.projections.length)
    for (const p of this.projections) {
      more = p.engine.warm(share) || more
      settle(p)
    }
    this.resumeViews()
    // Figures of the views that publish them (iv:measured): a write to the views graph.
    const figures = this.views.figures()
    if (figures.add.length || figures.remove.length) this.write(VIEW_GRAPH, figures)
    // What time is left goes to the store's own upkeep (e.g. compaction, compiling queries); at
    // least a millisecond, so that warming which overruns its slices does not starve it.
    const left = Math.max(1, until - performance.now())
    more = (this.store.maintain?.(left) ?? false) || more
    return more
  }

  /**
   * Evaluate, for up to `budgetMs`, the parked work under the given components - what
   * the person shows intent to see (see sem:OnIntent) - where their warmth allows it
   * (on intent or eager). Returns whether work remains.
   */
  preload(contents: ReadonlySet<string>, budgetMs: number): boolean {
    this.intent = contents
    let more = false
    for (const p of this.projections) {
      const warmth = this.warmth.get(p.engine)
      if (!warmth) continue
      const allowed = (focus: string) => {
        const w = warmth(focus)
        return (w === SEM.OnIntent || w === SEM.Eager) && this.intended(p, focus)
      }
      more = p.engine.warm(budgetMs / this.projections.length, 4, allowed) || more
      settle(p)
    }
    // Views suspended while hidden (iv:whileShown) whose components this built: rebuilt, and
    // what they project is evaluated in the next slice.
    return this.resumeViews() || more
  }

  /** The contents of the last preload: what the person last showed intent to see. */
  private intent?: ReadonlySet<string>

  /** Whether a focus is under what the person last showed intent to see, in a projection. */
  private intended(p: Projection, focus: string): boolean {
    const contents = this.intent
    return !!contents && p.index.nearest(focus, (key) => (contents.has(key) ? true : undefined)) === true
  }

  // --- stalls (src/engine/watchdog.ts) ---------------------------------------------------------------

  /** (rule, focus) pairs a suspended turn holds, in every projection. */
  get held(): number {
    return this.watchdog.held
  }

  /** Pairs a stopped stall left out of date, in every projection. */
  get stopped(): number {
    let n = 0
    for (const p of this.projections) n += p.engine.stoppedCount
    return n
  }

  /**
   * Continue a stall for one slice of at most `sliceMs` (the host yields between slices, so the
   * page stays responsive and shows progress). Continued to the end, the result is the same as a
   * turn that was never suspended. Returns whether work is still held.
   */
  continueStall(sliceMs = 50): boolean {
    const more = this.watchdog.turn(
      () => {
        let more = false
        for (const p of this.projections) {
          if (p.engine.heldCount) more = p.engine.continueHeld() || more
          settle(p)
        }
        this.resumeViews()
        return more || this.watchdog.held > 0
      },
      undefined,
      sliceMs,
    )
    if (!more) this.watchdog.resolve()
    for (const l of this.refreshListeners) l()
    return more
  }

  /**
   * Stop a stall: its held work is dropped (Engine.stopHeld). Every derived triple still comes
   * from a complete evaluation; the pairs not evaluated keep their output from before the stall
   * and are counted as stopped (out of date) until their input changes or retryStopped(). Input,
   * views and the change log are not rolled back: what the stalled turn applied stays applied.
   * Returns how many pairs are out of date.
   */
  stopStall(): number {
    for (const p of this.projections) if (p.engine.heldCount) p.engine.stopHeld()
    this.watchdog.resolve()
    for (const l of this.refreshListeners) l()
    return this.stopped
  }

  /** Evaluate what a stopped stall left out of date, as a turn of its own (it may stall again). */
  retryStopped(): void {
    this.watchdog.turn(() => {
      for (const p of this.projections) p.engine.retryStopped(), settle(p)
    })
    for (const l of this.refreshListeners) l()
  }

  /** Apply a change to the input graph (default graph) and update every projection. */
  apply(delta: Delta): Delta {
    const effective: Delta = { add: [], remove: [] }
    const f = this.store.factory
    const dg = f.defaultGraph()
    for (const q of delta.remove) {
      const g = f.quad(q.subject, q.predicate, q.object, dg)
      if (this.store.has(g)) this.store.delete(g), effective.remove.push(g)
    }
    for (const q of delta.add) {
      const g = f.quad(q.subject, q.predicate, q.object, dg)
      if (!this.store.has(g)) this.store.add(g), effective.add.push(g)
    }
    this.changed(undefined, effective)
    return effective
  }
}

/**
 * Newly shown components render, which may show more: repeat until nothing new
 * is demanded. Parked work is only re-checked when visibility changed, so the
 * cost of a change does not grow with how much of the app is hidden.
 */
const seenVisibility = new WeakMap<Projection, number>()
function settle(p: Projection) {
  for (let i = 0; i < 64; i++) {
    if (seenVisibility.get(p) === p.index.visibility) return
    seenVisibility.set(p, p.index.visibility)
    if (!p.engine.resume()) return
  }
}
