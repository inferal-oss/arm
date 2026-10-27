// The heartbeat (ontology/layers/watchdog.ttl, sem:heartbeat and sem:hangAfter). The page tells a
// Web Worker it is alive every 250 ms, and the engine does too while a long turn runs (with the
// rule and focus it is evaluating). The worker cannot interrupt the page, and nothing can render
// while the page is blocked; but it notices the gap, and once beats come back it reports the hang:
// when it started, how long it lasted, and what the page last said it was doing. A hang that never
// ends is still known to the worker (and in its last message, if the page ever answers).
//
// HeartbeatWatch is the worker's logic, on an injected clock (tests use a fake one); the worker
// (heartbeat.worker.ts) runs it on a timer; Heartbeat is the page's side.

/** What the page says with a beat: what it is doing (cheap: the watchdog's published rule and focus). */
export interface Beat {
  /** Milliseconds since the epoch. */
  at: number
  rule?: string
  focus?: string
}

/** A hang the worker noticed: no beat for longer than the threshold. */
export interface Hang {
  /** The last beat before it, and the first after it (epoch ms). */
  start: number
  end: number
  /** What the page last said it was doing. */
  last: Beat
}

/**
 * The worker's logic: beats in, hangs out. `check(now)` runs on the worker's own timer; a gap
 * over `hangAfter` is a hang in progress, reported (once) with the first beat after it.
 */
export class HeartbeatWatch {
  private last?: Beat
  /** The hang in progress: noticed by check() while the page is silent. */
  private open?: { start: number; last: Beat }

  constructor(public hangAfter = 2000) {}

  /** A beat arrived. Returns the hang it ends, if there was one. */
  beat(b: Beat): Hang | undefined {
    const prev = this.last
    this.last = b
    const open = this.open
    this.open = undefined
    if (open) return { start: open.start, end: b.at, last: open.last }
    // The worker's own timer may have been throttled too: a long gap between two beats is a hang.
    if (prev && b.at - prev.at > this.hangAfter) return { start: prev.at, end: b.at, last: prev }
    return undefined
  }

  /** The worker's timer: whether a hang is in progress now (the page has been silent too long). */
  check(now: number): { start: number; last: Beat } | undefined {
    if (!this.open && this.last && now - this.last.at > this.hangAfter) this.open = { start: this.last.at, last: this.last }
    return this.open
  }
}

/** Messages between the page and the worker. */
export type ToWorker = { kind: 'beat'; beat: Beat } | { kind: 'config'; hangAfter: number }
export type FromWorker = { kind: 'hang'; hang: Hang }

/**
 * The page's side: beats on a timer (and from the watchdog's pulses, while a long turn runs), and
 * the hangs the worker reports. Without Worker support it watches its own timer's lateness, which
 * notices the same hangs once they are over.
 */
export class Heartbeat {
  private worker?: { postMessage(m: ToWorker): void; terminate(): void; onmessage: ((e: { data: FromWorker }) => void) | null }
  private timer?: ReturnType<typeof setInterval>
  private lastLocal?: Beat

  constructor(
    private readonly opts: {
      intervalMs?: number
      hangAfter?: number
      onHang: (hang: Hang) => void
      /** Makes the worker (none: the page watches its own timer). */
      worker?: () => Heartbeat['worker']
      now?: () => number
      /** What the page is doing now (the watchdog's published rule and focus). */
      doing?: () => { rule?: string; focus?: string }
    },
  ) {}

  start(): () => void {
    const now = this.opts.now ?? Date.now
    try {
      this.worker = this.opts.worker?.()
    } catch {
      this.worker = undefined
    }
    if (this.worker) {
      this.worker.onmessage = (e) => e.data.kind === 'hang' && this.opts.onHang(e.data.hang)
      this.worker.postMessage({ kind: 'config', hangAfter: this.opts.hangAfter ?? 2000 })
    }
    const local = new HeartbeatWatch(this.opts.hangAfter ?? 2000)
    this.timer = setInterval(() => {
      const b = { at: now(), ...this.opts.doing?.() }
      if (this.worker) this.worker.postMessage({ kind: 'beat', beat: b })
      else {
        const hang = local.beat(b)
        if (hang) this.opts.onHang(hang)
      }
      this.lastLocal = b
    }, this.opts.intervalMs ?? 250)
    return () => this.stop()
  }

  /** A beat from inside a long turn (the watchdog's pulse): what is being evaluated now. */
  pulse(rule?: string, focus?: string): void {
    const b = { at: (this.opts.now ?? Date.now)(), rule, focus }
    this.worker?.postMessage({ kind: 'beat', beat: b })
    this.lastLocal = b
  }

  stop(): void {
    if (this.timer !== undefined) clearInterval(this.timer)
    this.timer = undefined
    this.worker?.terminate()
    this.worker = undefined
    void this.lastLocal
  }
}
