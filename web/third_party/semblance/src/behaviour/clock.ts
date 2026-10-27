// Time, as the host keeps it. Whatever happens "later" in Semblance - a scheduled event
// (beh:dueAt, src/behaviour/schedule.ts), an expiring window of a maintained view - asks a
// Clock for the time and for a callback at a time, so tests can move time by hand
// (ManualClock) and nothing depends on the wall clock directly. Share one clock per host.

export interface Clock {
  /** Milliseconds since the epoch. */
  now(): number
  /** Run `fn` at clock time `at` (or as soon as possible if it has passed); returns a cancel function. */
  schedule(at: number, fn: () => void): () => void
}

/**
 * The wall clock. It sets timers only where there is a page to update (a browser); elsewhere
 * (Node, tests that did not ask for time to pass) nothing scheduled ever fires by itself.
 */
export const systemClock = (): Clock => ({
  now: () => Date.now(),
  schedule(at, fn) {
    if (typeof requestAnimationFrame === 'undefined') return () => {}
    const t = setTimeout(fn, Math.max(0, at - Date.now()))
    return () => clearTimeout(t)
  },
})

/** A clock tests move by hand: advance() runs, in time order, every callback that has come due. */
export class ManualClock implements Clock {
  private pending: { at: number; fn: () => void }[] = []
  constructor(public t = Date.parse('2026-01-01T09:00:00Z')) {}
  now = () => this.t
  schedule(at: number, fn: () => void) {
    const p = { at, fn }
    this.pending.push(p)
    return () => void (this.pending = this.pending.filter((x) => x !== p))
  }
  /** Move time forward by `ms`, running what comes due on the way (also what those schedule). */
  advance(ms = 0): void {
    const end = this.t + ms
    for (;;) {
      const next = this.pending.filter((p) => p.at <= end).sort((a, b) => a.at - b.at)[0]
      if (!next) break
      this.pending = this.pending.filter((x) => x !== next)
      this.t = Math.max(this.t, next.at)
      next.fn()
    }
    this.t = end
  }
}
