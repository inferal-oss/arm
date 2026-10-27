// The stall effect (ontology/layers/watchdog.ttl): the host side of the watchdog. It reads the
// app's sem:Watchdog (budget, slice, heartbeat), and
//   - when a turn is suspended (src/engine/watchdog.ts), records a sem:Stall in the performance
//     graph and raises sem:Stalled on the watchdog, whose reaction opens the stall dialog;
//   - does what the person decides (sem:decision, written by the dialog's buttons' reaction):
//     continue in slices with a yield between them, updating the record's progress; stop; retry;
//   - logs what nothing could interrupt - heartbeat gaps, long tasks, long animation frames -
//     blamed on the turn (or DOM patching) they overlap, and raises sem:Unresponsive for hangs
//     over sem:hangAfter, which the announcements layer shows.
// Every stall is a member of the watchdog's log (sem:StallLog), bounded by its retention policy.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import { NS, PERF_GRAPH, RDF_TYPE, XSD_INTEGER } from '../ns.ts'
import { parseDuration, type Delta } from '../rdf.ts'
import type { Runtime } from '../runtime.ts'
import type { Stall, Turn, Work } from '../engine/watchdog.ts'
import { WD } from '../components/stall.ts'
import type { EventBus } from './events.ts'
import { type Hang, Heartbeat } from './heartbeat.ts'

const XSD_DATETIME = NS.xsd + 'dateTime'
const KIND: Record<Work['kind'] | 'render', string> = { rule: WD.RuleWork, view: WD.ViewWork, reaction: WD.ReactionWork, query: WD.QueryWork, render: NS.sem + 'RenderWork' }

export interface StallMonitorOptions {
  /** The app's sem:Watchdog. */
  watchdog: string
  /** Raises sem:Stalled, sem:StallSettled and sem:Unresponsive (without one, a stall is continued at once). */
  bus?: EventBus
  /** Patch the page after a write (and between slices). */
  flush?: () => void
  /** Called after an event this effect raised was handled (the host's usual onHandled work). */
  onHandled?: () => void
  /** The next slice, after the page had a turn (a macrotask by default). */
  yieldTo?: (next: () => void) => void
  /** How a stall waits for the end of the turn that found it (a microtask by default). */
  defer?: (fn: () => void) => void
  /** Epoch milliseconds (sem:at). */
  now?: () => number
  /** Start a heartbeat worker (browsers; heartbeat.worker.ts). */
  worker?: () => Worker
}

/** A hang nothing could interrupt, on the watchdog's clock (performance.now in a page). */
export interface Observation {
  detector: string
  start: number
  duration: number
  /** What the page said it was doing (the heartbeat's last beat), or the frame's script. */
  rule?: string
  focus?: string
  script?: string
}

export class StallMonitor {
  private readonly f
  private readonly wd: NamedNode
  private records = 0
  private current?: NamedNode
  private continuing = false
  private detach: (() => void)[] = []
  /** Retained log members, oldest first, with their subjects. */
  private members: NamedNode[] = []
  /** Recent DOM patches (the watchdog's clock), to blame what happened outside turns. */
  private renders: { start: number; end: number }[] = []
  /** When this effect last wrote: a long task of its own writing is not logged (no feedback). */
  private writing: { start: number; end: number }[] = []
  /** Observations already logged (an interval, to merge a heartbeat gap with the long task it was). */
  private logged: { start: number; end: number; node: NamedNode }[] = []

  constructor(
    readonly runtime: Runtime,
    private readonly opts: StallMonitorOptions,
  ) {
    this.f = runtime.store.factory
    this.wd = this.f.namedNode(opts.watchdog)
  }

  private nn(iri: string) {
    return this.f.namedNode(iri)
  }

  private duration(p: string, fallback: number): number {
    const v = this.runtime.store.match(this.wd, this.nn(p), null)[0]?.object.value
    const ms = v ? parseDuration(v) : undefined
    return ms ?? fallback
  }

  get budgetMs() {
    return this.duration(WD.budget, 1500)
  }
  get sliceMs() {
    return this.duration(WD.slice, 50)
  }
  get hangAfterMs() {
    return this.duration(WD.hangAfter, 2000)
  }
  get logAfterMs() {
    return this.duration(WD.logAfter, 500)
  }

  /** Follow the runtime's stalls and the person's decisions. Returns a function that stops. */
  attach(): () => void {
    const wd = this.runtime.watchdog
    wd.budgetMs = this.budgetMs
    const defer = this.opts.defer ?? ((fn) => queueMicrotask(fn))
    this.detach.push(wd.onStall((s) => defer(() => this.stalled(s))))
    this.detach.push(
      this.runtime.onInput((graph, delta) => {
        if (graph !== undefined) return
        for (const q of delta.add) {
          if (q.subject.equals(this.wd) && q.predicate.value === WD.decision) defer(() => this.decide(q.object.value))
          if (q.predicate.value === NS.c + 'open' && q.object.value === 'false' && this.isDialog(q.subject)) defer(() => this.dismissed())
          if (q.subject.equals(this.wd) && (q.predicate.value === WD.budget)) wd.budgetMs = this.budgetMs
        }
      }),
    )
    // Out-of-date pairs heal when their input changes again: the count follows (after the turn).
    let counted = 0
    this.detach.push(
      wd.onTurn(() => {
        // Later turns may have evaluated everything that was held (its input changed again): the stall is over.
        if (this.current && !this.continuing && wd.stall && !this.runtime.held && !this.settling) {
          this.settling = true
          defer(() => {
            this.settling = false
            if (!this.current || this.continuing || this.runtime.held) return
            wd.resolve()
            this.settle(WD.Continued)
          })
        }
        if (this.runtime.stopped === counted || this.outOfDateQueued) return
        this.outOfDateQueued = true
        defer(() => {
          this.outOfDateQueued = false
          counted = this.runtime.stopped
          this.writeOutOfDate()
          this.opts.flush?.()
        })
      }),
    )
    // A stall that happened before (the first projection) is taken up now.
    if (wd.stall) defer(() => this.stalled(wd.stall!))
    return () => this.dispose()
  }

  /** Start the heartbeat worker and the long-task observers (browsers). */
  observe(): () => void {
    const wd = this.runtime.watchdog
    const beat = new Heartbeat({
      intervalMs: this.duration(WD.heartbeat, 250),
      // Gaps worth logging (announced only past sem:hangAfter): where frame observers are missing, this is all there is.
      hangAfter: this.logAfterMs,
      worker: this.opts.worker as never,
      now: () => Date.now(),
      doing: () => ({ rule: wd.busy ? wd.rule : undefined, focus: wd.busy ? wd.focus : undefined }),
      onHang: (h) => this.hang(h),
    })
    this.detach.push(beat.start())
    wd.onPulse = (rule, focus) => beat.pulse(rule, focus)
    const PO = (globalThis as { PerformanceObserver?: typeof PerformanceObserver }).PerformanceObserver
    const supported = PO?.supportedEntryTypes ?? []
    for (const type of ['long-animation-frame', 'longtask'] as const) {
      if (!PO || !supported.includes(type)) continue
      // Long animation frames say more (scripts); long tasks only where they are not available.
      if (type === 'longtask' && supported.includes('long-animation-frame')) continue
      const po = new PO((list) => {
        for (const e of list.getEntries()) {
          if (e.duration < this.logAfterMs) continue
          const scripts = (e as PerformanceEntry & { scripts?: { invoker?: string; sourceFunctionName?: string; sourceURL?: string; sourceCharPosition?: number; duration: number }[] }).scripts ?? []
          const top = [...scripts].sort((a, b) => b.duration - a.duration)[0]
          const script = top ? `${top.invoker ?? ''} ${top.sourceFunctionName ?? ''} ${(top.sourceURL ?? '').replace(/^.*\//, '')}:${top.sourceCharPosition ?? ''} (${Math.round(top.duration)} ms)`.trim() : undefined
          this.observed({ detector: type === 'longtask' ? WD.LongTask : WD.LongFrame, start: e.startTime, duration: e.duration, script })
        }
      })
      try {
        po.observe({ type, buffered: true })
        this.detach.push(() => po.disconnect())
      } catch {}
    }
    return () => this.dispose()
  }

  /** Time a DOM patch (the host's flush), so a hang outside every turn can be blamed on it. */
  rendering<T>(fn: () => T): T {
    const clock = this.runtime.watchdog.clock
    const start = clock()
    try {
      return fn()
    } finally {
      const end = clock()
      if (end - start > 16) {
        this.renders.push({ start, end })
        if (this.renders.length > 32) this.renders.shift()
      }
    }
  }

  dispose(): void {
    for (const d of this.detach.splice(0)) d()
    this.runtime.watchdog.onPulse = undefined
  }

  // --- suspended stalls --------------------------------------------------------------------------

  private stalled(s: Stall) {
    if (this.current || this.runtime.watchdog.stall !== s) return
    this.current = this.nn(`${this.wd.value}/~stall-${++this.records}`)
    this.write(this.recordOf(this.current, s, WD.Suspended, s.interrupted ? WD.QueryDeadline : WD.BudgetCheck), true)
    if (!this.opts.bus || !this.dialog()) return this.decide(WD.Continue)
    this.raise(WD.Stalled, s)
  }

  private dialog(): Term | undefined {
    return this.runtime.store.match(this.wd, this.nn(WD.stallDialog), null)[0]?.object
  }

  private isDialog(t: Term) {
    return this.dialog()?.equals(t) ?? false
  }

  /** The dialog was closed without a decision: carry on, in the background. */
  private dismissed() {
    if (this.current && !this.continuing && this.runtime.held) this.decide(WD.Continue)
  }

  /** Do what the person decided. */
  decide(choice: string): void {
    if (choice === WD.Retry) {
      this.runtime.retryStopped()
      this.writeOutOfDate()
      this.opts.flush?.()
      return
    }
    if (!this.current) return
    if (choice === WD.Stop) {
      this.continuing = false
      this.runtime.stopStall()
      this.settle(WD.Stopped)
    } else if (choice === WD.Continue && !this.continuing) {
      this.continuing = true
      this.slice()
    }
  }

  private slice() {
    if (!this.continuing || !this.current) return
    const more = this.runtime.continueStall(this.sliceMs)
    const s = this.runtime.watchdog.stall
    if (more && s) {
      this.write(this.recordOf(this.current, s, WD.Continuing))
      this.opts.flush?.()
      ;(this.opts.yieldTo ?? ((next) => setTimeout(next, 0)))(() => this.slice())
    } else {
      this.continuing = false
      this.settle(WD.Continued)
    }
  }

  private settle(state: string) {
    const node = this.current!
    const store = this.runtime.store
    const g = this.nn(PERF_GRAPH)
    // The final state; the watchdog no longer has a stall in progress.
    const stateQuads = store.match(node, this.nn(WD.stallState), null, g)
    this.write({
      remove: [...stateQuads, ...store.match(this.wd, this.nn(WD.stall), null, g)],
      add: [this.f.quad(node, this.nn(WD.stallState), this.nn(state))],
    })
    this.writeOutOfDate()
    this.current = undefined
    this.raise(WD.StallSettled)
    this.opts.flush?.()
  }

  private outOfDateQueued = false
  private settling = false

  private writeOutOfDate() {
    const n = this.runtime.stopped
    const g = this.nn(PERF_GRAPH)
    const old = this.runtime.store.match(this.wd, this.nn(WD.outOfDate), null, g)
    if (old.length === (n ? 1 : 0) && (!n || old[0]!.object.value === String(n))) return
    this.write({ remove: old, add: n ? [this.f.quad(this.wd, this.nn(WD.outOfDate), this.integer(n))] : [] })
  }

  private raise(type: string, s?: Stall) {
    const bus = this.opts.bus
    if (!bus) return
    const about: [string, Term][] = []
    if (s?.culprit) about.push([WD.culprit, this.nn(s.culprit.iri)])
    if (this.current) about.push([NS.sem + 'about', this.current])
    bus.emit(type, this.wd.value, { about: about as never })
    this.opts.onHandled?.()
  }

  /** The record of a stall (replacing what it said before), as a change of the performance graph. */
  private recordOf(node: NamedNode, s: Stall, state: string, detector?: string): Delta {
    const store = this.runtime.store
    const g = this.nn(PERF_GRAPH)
    const keep = new Set([WD.detectedBy, WD.at, WD.startedBy, WD.eventType, RDF_TYPE, NS.sem + 'sequence'])
    const remove = store.match(node, null, null, g).filter((q) => !keep.has(q.predicate.value))
    const add: Quad[] = []
    const t = (p: string, o: Term) => add.push(this.f.quad(node, this.nn(p), o))
    const fresh = !store.match(node, this.nn(RDF_TYPE), null, g).length
    if (fresh) {
      t(RDF_TYPE, this.nn(WD.Stall))
      t(WD.at, this.f.literal(new Date((this.opts.now ?? Date.now)()).toISOString(), this.nn(XSD_DATETIME)))
      t(NS.sem + 'sequence', this.integer(this.records))
      if (detector) t(WD.detectedBy, this.nn(detector))
      if (s.event) t(WD.startedBy, this.nn(s.event))
      if (s.eventType) t(WD.eventType, this.nn(s.eventType))
      if (!store.match(this.wd, this.nn(WD.stall), node, g).length) {
        remove.push(...store.match(this.wd, this.nn(WD.stall), null, g))
        add.push(this.f.quad(this.wd, this.nn(WD.stall), node))
      }
    }
    t(WD.stallState, this.nn(state))
    t(WD.done, this.integer(s.done))
    t(WD.pending, this.integer(s.pending))
    t(WD.spentMs, this.integer(Math.round(s.spent)))
    this.culprit(add, node, s.culprit)
    return { remove, add }
  }

  private culprit(add: Quad[], node: NamedNode, w: Work | undefined) {
    if (!w) return
    add.push(this.f.quad(node, this.nn(WD.culprit), this.nn(w.iri)), this.f.quad(node, this.nn(WD.workKind), this.nn(KIND[w.kind])))
    add.push(this.f.quad(node, this.nn(WD.culpritMs), this.integer(Math.round(w.ms))))
    if (w.focus) add.push(this.f.quad(node, this.nn(WD.culpritFocus), this.nn(w.focus.split(' ')[0]!)))
  }

  // --- observed hangs ------------------------------------------------------------------------------

  /** A gap the heartbeat worker reported (epoch ms), on the watchdog's clock. */
  private hang(h: Hang) {
    const offset = this.runtime.watchdog.clock() - Date.now()
    this.observed({ detector: WD.Heartbeat, start: h.start + offset, duration: h.end - h.start, rule: h.last.rule, focus: h.last.focus })
  }

  /**
   * Log a hang nothing could interrupt: blamed on the turn it overlaps most (its slowest work),
   * else on DOM patching. A turn the watchdog already suspended is its own record; a hang that
   * was this effect's own writing is not logged. Overlapping observations (a heartbeat gap and the
   * long task it was) are one record.
   */
  observed(o: Observation): void {
    const end = o.start + o.duration
    const overlap = (a: { start: number; end: number }) => Math.max(0, Math.min(end, a.end) - Math.max(o.start, a.start))
    if (this.writing.some((w) => overlap(w) > o.duration / 2)) return
    const turns = this.runtime.watchdog.turns.filter((t) => overlap(t) > 0)
    const turn = turns.sort((a, b) => overlap(b) - overlap(a))[0] as Turn | undefined
    if (turn?.suspended) return
    const already = this.logged.find((l) => overlap(l) > 0)
    let work: Work | undefined = turn?.slowest
    if (!work && o.rule) work = { kind: 'rule', iri: o.rule, focus: o.focus, count: 1, ms: o.duration }
    const rendered = !work && this.renders.some((r) => overlap(r) > o.duration / 4)
    if (already) {
      // The same hang seen twice: keep the longer duration on the first record.
      if (o.duration > already.end - already.start) {
        const g = this.nn(PERF_GRAPH)
        const old = this.runtime.store.match(already.node, this.nn(WD.spentMs), null, g)
        this.write({ remove: old, add: [this.f.quad(already.node, this.nn(WD.spentMs), this.integer(Math.round(o.duration)))] })
        already.start = Math.min(already.start, o.start)
        already.end = Math.max(already.end, end)
      }
      return
    }
    const node = this.nn(`${this.wd.value}/~stall-${++this.records}`)
    const add: Quad[] = []
    const t = (p: string, obj: Term) => add.push(this.f.quad(node, this.nn(p), obj))
    t(RDF_TYPE, this.nn(WD.Stall))
    t(WD.stallState, this.nn(WD.Observed))
    t(WD.detectedBy, this.nn(o.detector))
    t(WD.at, this.f.literal(new Date((this.opts.now ?? Date.now)() - (this.runtime.watchdog.clock() - o.start)).toISOString(), this.nn(XSD_DATETIME)))
    t(NS.sem + 'sequence', this.integer(this.records))
    t(WD.spentMs, this.integer(Math.round(o.duration)))
    if (turn?.event) t(WD.startedBy, this.nn(turn.event))
    if (turn?.eventType) t(WD.eventType, this.nn(turn.eventType))
    if (turn) t(WD.done, this.integer(turn.evaluations))
    this.culprit(add, node, work)
    if (rendered) t(WD.workKind, this.nn(KIND.render))
    if (o.script) t(NS.sem + 'script', this.f.literal(o.script))
    this.logged.push({ start: o.start, end, node })
    if (this.logged.length > 16) this.logged.shift()
    this.write({ remove: [], add }, true, node)
    if (o.duration >= this.hangAfterMs) this.raise(WD.Unresponsive)
    this.opts.flush?.()
  }

  // --- writing -----------------------------------------------------------------------------------

  /** Write to the performance graph; `member` appends the record to the log (retiring what retention drops). */
  private write(delta: Delta, member = false, node = this.current) {
    const clock = this.runtime.watchdog.clock
    const start = clock()
    if (member && node) {
      const log = this.nn(this.runtime.store.match(this.wd, this.nn(WD.stallLog), null)[0]?.object.value ?? WD.StallLog)
      delta.add.push(this.f.quad(log, this.nn(NS.tree + 'member'), node))
      this.members.push(node)
      const keep = this.retained(log)
      const g = this.nn(PERF_GRAPH)
      while (this.members.length > keep) {
        const old = this.members.shift()!
        delta.remove.push(...this.runtime.store.match(log, this.nn(NS.tree + 'member'), old, g), ...this.runtime.store.match(old, null, null, g))
      }
    }
    this.runtime.write(PERF_GRAPH, delta)
    this.writing.push({ start, end: clock() })
    if (this.writing.length > 16) this.writing.shift()
  }

  private retained(log: NamedNode): number {
    const store = this.runtime.store
    for (const v of store.match(log, this.nn(NS.tree + 'view'), null))
      for (const p of store.match(v.object as NamedNode, this.nn(NS.ldes + 'retentionPolicy'), null)) {
        const amount = store.match(p.object as NamedNode, this.nn(NS.ldes + 'amount'), null)[0]
        if (amount) return Number(amount.object.value)
      }
    return 50
  }

  private integer(n: number) {
    return this.f.literal(String(n), this.nn(XSD_INTEGER))
  }
}
