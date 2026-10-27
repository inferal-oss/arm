// Things due later (ontology/layers/behaviour.ttl, beh:dueAt): a host effect that turns a
// node's due time into one recorded beh:Due event addressed to that node, handled by ordinary
// reactions. It keeps one timer, for the earliest due node, on the host's clock (the event
// bus's, so beh:at and beh:dueAt agree, and tests move both by hand with a ManualClock).
import type { NamedNode, Quad } from '@rdfjs/types'
import { NS } from '../ns.ts'
import type { Delta } from '../rdf.ts'
import type { Clock } from './clock.ts'
import type { EventBus, Handled } from './events.ts'

const DUE_AT = NS.beh + 'dueAt'
const DUE_WHILE_SHOWN = NS.beh + 'dueWhileShown'
const DUE = NS.beh + 'Due'

export interface SchedulerOptions {
  /** Whether a component is shown (for beh:dueWhileShown); without it, nothing waits. */
  shown?: (component: string) => boolean
  /** After each beh:Due event a reaction handled (e.g. to patch the DOM). */
  onHandled?: (h: Handled) => void
}

export class Scheduler {
  /** Due nodes and when (ms); kept in step with the input graph's beh:dueAt triples. */
  private readonly due = new Map<string, number>()
  /** Due nodes past their time, waiting to be shown. */
  private readonly waiting = new Set<string>()
  private timer?: { at: number; cancel: () => void }
  private recheck?: () => void
  private firing = false
  private detach?: () => void

  constructor(
    private readonly bus: EventBus,
    private readonly opts: SchedulerOptions = {},
  ) {}

  get clock(): Clock {
    return this.bus.clock
  }

  /** Follow the input graph's due times from now on (what is due already is picked up). */
  attach(): () => void {
    const store = this.bus.runtime.store
    const f = store.factory
    for (const q of store.match(null, f.namedNode(DUE_AT), null, f.defaultGraph())) this.track(q, 1)
    this.arm()
    const off = this.bus.runtime.onInput((graph, delta) => this.changed(graph, delta))
    this.detach = () => {
      off()
      this.timer?.cancel()
      this.recheck?.()
    }
    return this.detach
  }

  private track(q: Quad, sign: 1 | -1) {
    const node = q.subject.value
    if (sign < 0) {
      if (this.due.get(node) === Date.parse(q.object.value)) this.due.delete(node), this.waiting.delete(node)
      return
    }
    const at = Date.parse(q.object.value)
    if (Number.isFinite(at)) this.due.set(node, at), this.waiting.delete(node)
  }

  private changed(graph: string | undefined, delta: Delta) {
    if (graph !== undefined) return
    let touched = false
    for (const q of delta.remove) if (q.predicate.value === DUE_AT) this.track(q, -1), (touched = true)
    for (const q of delta.add) if (q.predicate.value === DUE_AT) this.track(q, 1), (touched = true)
    if (touched && !this.firing) this.arm()
    // Something waits to be shown, and the input changed (a page, a tab, a case): look again
    // once this change has been projected.
    if (this.waiting.size && !this.recheck) this.recheck = this.clock.schedule(this.clock.now(), () => ((this.recheck = undefined), this.fire()))
  }

  /** One timer, for the earliest due node that is not waiting to be shown. */
  private arm() {
    let earliest = Infinity
    for (const [node, at] of this.due) if (!this.waiting.has(node) && at < earliest) earliest = at
    if (this.timer && this.timer.at === earliest) return
    this.timer?.cancel()
    this.timer = undefined
    if (earliest === Infinity) return
    this.timer = { at: earliest, cancel: this.clock.schedule(earliest, () => ((this.timer = undefined), this.fire())) }
  }

  /** Every node whose time has come, earliest first: one beh:Due event each. */
  private fire() {
    if (this.firing) return
    this.firing = true
    try {
      const store = this.bus.runtime.store
      const f = store.factory
      const now = this.clock.now()
      const ready = [...this.due].filter(([, at]) => at <= now).sort((a, b) => a[1] - b[1] || (a[0] < b[0] ? -1 : 1))
      for (const [node, at] of ready) {
        const subject = f.namedNode(node)
        const shownBy = store.match(subject, f.namedNode(DUE_WHILE_SHOWN), null, f.defaultGraph())[0]?.object
        if (shownBy && this.opts.shown && !this.opts.shown(shownBy.value)) {
          this.waiting.add(node)
          continue
        }
        this.waiting.delete(node)
        const handled = this.bus.emit(DUE, node)
        // Spent, unless a reaction set a new time.
        const left = store.match(subject, f.namedNode(DUE_AT), null, f.defaultGraph()).filter((q) => Date.parse(q.object.value) === at)
        if (left.length) this.bus.runtime.apply({ remove: left, add: [] })
        if (this.due.get(node) === at) this.due.delete(node)
        if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) this.opts.onHandled?.(handled)
      }
    } finally {
      this.firing = false
    }
    this.arm()
  }

  /** The nodes due, and when (for tests and the inspector). */
  pending(): { node: NamedNode; at: number; waiting: boolean }[] {
    const f = this.bus.runtime.store.factory
    return [...this.due].map(([node, at]) => ({ node: f.namedNode(node), at, waiting: this.waiting.has(node) }))
  }
}
