// The cooperative watchdog (ontology/layers/watchdog.ttl). A turn is everything one input causes:
// an event's reactions, the tick's maintained views, and every projection's rule evaluations. A
// turn has a time budget (sem:budget, 1.5 s by default). The engine asks the watchdog between
// evaluations whether the turn is over budget; when it is, the engine sets the rest of its pending
// (rule, focus) pairs aside (it holds them) and returns, so the page can render and answer again.
// What was running is published here cheaply (the rule and focus being evaluated, and the slowest
// piece of work of the turn), so a stall, and a hang the page could not interrupt, can be blamed.
//
// The watchdog decides nothing about what happens next: the runtime continues held work in slices
// (Runtime.continueStall) or stops it (Runtime.stopStall), and the host asks the person (the stall
// dialog, src/behaviour/stall.ts).

/** What kind of work: a rule evaluation, a maintained view's step, a reaction, a single query. */
export type WorkKind = 'rule' | 'view' | 'reaction' | 'query'

/** One piece of work, timed. */
export interface Work {
  kind: WorkKind
  /** The rule, view or reaction IRI. */
  iri: string
  /** The focus it was evaluated for (the first, when it covered several). */
  focus?: string
  /** How many foci (or rows) it covered. */
  count: number
  ms: number
}

/** A finished turn, kept in a short ring so a hang noticed later can be matched to what ran. */
export interface Turn {
  /** On the watchdog's clock. */
  start: number
  end: number
  /** The event that started it, if an event did. */
  event?: string
  eventType?: string
  slowest?: Work
  /** Evaluations the engines made in it. */
  evaluations: number
  /** Whether it ended with work held (suspended). */
  suspended: boolean
}

/** The stall in progress: a turn that went over budget, until its held work is continued or stopped. */
export interface Stall {
  /** Numbered per watchdog, from 1. */
  n: number
  /** When the turn that stalled started, and when it was suspended (the watchdog's clock). */
  start: number
  suspendedAt: number
  event?: string
  eventType?: string
  /** The slowest piece of work, and what was running when the budget ran out. */
  culprit?: Work
  /** Evaluations done since the stalled turn started, and pairs still held. */
  done: number
  pending: number
  /** Milliseconds spent working on it (excluding the time it waited). */
  spent: number
  /** Whether a single query was interrupted (store deadline) rather than the turn suspended between evaluations. */
  interrupted: boolean
}

export type StallListener = (stall: Stall) => void

export class Watchdog {
  /**
   * The budget of a turn, in ms. Infinity (the default) never suspends: a host that can ask the
   * person (src/behaviour/stall.ts) sets it from the app's sem:budget (1.5 s by default).
   */
  budgetMs = Infinity
  /** The clock is read every this many evaluations (and at every rule batch): the per-evaluation cost is a counter. */
  every = 8
  /** Milliseconds, monotonic: performance.now by default; tests inject one. */
  clock: () => number = typeof performance !== 'undefined' ? () => performance.now() : () => Date.now()

  /**
   * Called with what is being evaluated at most every `pulseMs` while a turn runs, when the clock
   * is read anyway (the page's heartbeat: a hang that never ends names what it was doing).
   */
  onPulse?: (rule: string | undefined, focus: string | undefined) => void
  pulseMs = 250
  private lastPulse = -Infinity

  /** Published: the rule and focus being evaluated now (two assignments per evaluation). */
  rule?: string
  focus?: string

  private depth = 0
  private deadline = Infinity
  private counter = 0
  private turnStart = 0
  private turnEvent?: { iri: string; type: string }
  private slowest?: Work
  private evaluations = 0
  private turnSuspended = false
  /** Whether the open turn is a slice of continued work: running out of it yields, it is not a stall. */
  private slicing = false
  private holders = new Set<{ readonly heldCount: number }>()
  private listeners: StallListener[] = []
  private turnListeners: ((t: Turn) => void)[] = []
  private nextStall = 1
  /** Recent turns, newest last (at most `keep`). */
  readonly turns: Turn[] = []
  keep = 32
  /** The stall in progress, if any. */
  stall?: Stall

  /** Hear about a turn that went over budget (once per stall, when the turn has ended). */
  onStall(listener: StallListener): () => void {
    this.listeners.push(listener)
    return () => void (this.listeners = this.listeners.filter((l) => l !== listener))
  }

  /** Hear about every finished turn (the heartbeat matches hangs to them). */
  onTurn(listener: (t: Turn) => void): () => void {
    this.turnListeners.push(listener)
    return () => void (this.turnListeners = this.turnListeners.filter((l) => l !== listener))
  }

  /** Whether a turn is open. */
  get busy(): boolean {
    return this.depth > 0
  }

  /** Pairs held by every engine of this watchdog. */
  get held(): number {
    let n = 0
    for (const h of this.holders) n += h.heldCount
    return n
  }

  /**
   * Run `fn` as (part of) a turn. The outermost call starts the turn and its budget; `event`
   * names what started it. With `sliceMs`, the turn is a slice of continued work with that budget.
   */
  turn<T>(fn: () => T, event?: { iri: string; type: string }, sliceMs?: number): T {
    if (this.depth++ === 0) {
      this.turnStart = this.clock()
      this.slicing = sliceMs !== undefined
      this.deadline = this.turnStart + (sliceMs ?? this.budgetMs)
      this.turnEvent = event
      this.slowest = undefined
      this.evaluations = 0
      this.turnSuspended = false
      this.counter = 0
    } else if (event && !this.turnEvent) this.turnEvent = event
    try {
      return fn()
    } finally {
      if (--this.depth === 0) this.end()
    }
  }

  /** Name the event that started the open turn (if nothing named one yet). */
  startedBy(iri: string, type: string): void {
    if (this.depth > 0 && !this.turnEvent) this.turnEvent = { iri, type }
  }

  /** Between evaluations: whether the turn is over budget. Reads the clock every `every` calls. */
  check(): boolean {
    this.evaluations++
    if (++this.counter < this.every) return false
    this.counter = 0
    return this.read() > this.deadline
  }

  private read(): number {
    const t = this.clock()
    if (this.onPulse && t - this.lastPulse > this.pulseMs) (this.lastPulse = t), this.onPulse(this.rule, this.focus)
    return t
  }

  /** Evaluations done in one go (a bulk query answered `n` foci). */
  evaluated(n: number): void {
    this.evaluations += n
  }

  /**
   * Milliseconds left in the open turn, for a store's query deadline; undefined when there is no
   * turn, no budget, or while continuing a stall (the person chose to wait: queries run to the end).
   */
  remaining(): number | undefined {
    if (this.depth === 0 || this.slicing || this.deadline === Infinity) return undefined
    return Math.max(0, this.deadline - this.clock())
  }

  /** Whether the turn is over budget now (reads the clock). */
  due(): boolean {
    return this.depth > 0 && this.read() > this.deadline
  }

  /** The deadline of the open turn on the watchdog's clock (Infinity when none, or while continuing a stall). */
  get until(): number {
    return this.depth > 0 ? this.deadline : Infinity
  }

  /** Time a piece of work (cheap: a comparison). The slowest of a turn is its culprit. */
  note(kind: WorkKind, iri: string, focus: string | undefined, count: number, ms: number): void {
    if (!this.slowest || ms > this.slowest.ms) this.slowest = { kind, iri, focus, count, ms }
  }

  /** An engine set work aside: the turn is suspended. */
  suspended(holder: { readonly heldCount: number }, interrupted = false): void {
    this.holders.add(holder)
    this.turnSuspended = true
    if (interrupted && this.stall) this.stall.interrupted = true
    else if (interrupted) this.pendingInterrupt = true
  }
  private pendingInterrupt = false

  /** An engine has nothing held any more. */
  released(holder: { readonly heldCount: number }): void {
    if (!holder.heldCount) this.holders.delete(holder)
  }

  /** Forget the stall (its work was continued to the end or stopped). */
  resolve(): Stall | undefined {
    const s = this.stall
    this.stall = undefined
    this.pendingInterrupt = false
    for (const h of [...this.holders]) if (!h.heldCount) this.holders.delete(h)
    return s
  }

  private end() {
    const end = this.clock()
    const turn: Turn = {
      start: this.turnStart,
      end,
      event: this.turnEvent?.iri,
      eventType: this.turnEvent?.type,
      slowest: this.slowest,
      evaluations: this.evaluations,
      suspended: this.turnSuspended,
    }
    this.turns.push(turn)
    if (this.turns.length > this.keep) this.turns.splice(0, this.turns.length - this.keep)
    const held = this.held
    let fresh = false
    if (this.stall) {
      // Work on the stall in progress (a slice, or a turn of its own that held more).
      this.stall.done += this.evaluations
      this.stall.spent += end - this.turnStart
      this.stall.pending = held
      if (this.slowest && (!this.stall.culprit || this.slowest.ms > this.stall.culprit.ms)) this.stall.culprit = this.slowest
    } else if (held && this.turnSuspended && !this.slicing) {
      fresh = true
      this.stall = {
        n: this.nextStall++,
        start: this.turnStart,
        suspendedAt: end,
        event: this.turnEvent?.iri,
        eventType: this.turnEvent?.type,
        culprit: this.slowest ?? (this.rule ? { kind: 'rule', iri: this.rule, focus: this.focus, count: 1, ms: end - this.turnStart } : undefined),
        done: this.evaluations,
        pending: held,
        spent: end - this.turnStart,
        interrupted: this.pendingInterrupt,
      }
      this.pendingInterrupt = false
    }
    this.deadline = Infinity
    this.slicing = false
    // A turn that said what it was doing says it is done, so a hang after it is not blamed on it.
    if (this.onPulse && this.lastPulse >= this.turnStart) (this.lastPulse = end), this.onPulse(undefined, undefined)
    for (const l of this.turnListeners) l(turn)
    if (fresh) for (const l of this.listeners) l(this.stall!)
  }
}
