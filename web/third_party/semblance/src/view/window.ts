// Windows over a stream (docs/IVM.md §7.3-7.4): which of an LDES's members a view counts, as
// time passes or members arrive. Every window is a suffix of the stream's members ordered by
// (timestamp, member IRI):
//
//   sliding   members with t >= now - range
//   tumbling  members whose bucket (floor(t / size) * size, epoch-aligned) is one of the newest
//             `keep` up to now's: t >= bucket(now) - (keep - 1) * size; the bucket start can be a
//             column of the view (iv:bucketVariable)
//   count     the latest n members (no clock)
//
// "now" is the wall clock (one timer for the next expiry), or with event time the watermark:
// the greatest timestamp among the stream's members minus the lateness, so expiry follows the
// data and needs no timer (net:Capture's timestamps are milliseconds of the page, not dates).
//
// The index holds every member with a timestamp, whether or not the view's body has rows for
// it (a count window counts the stream's members, not the body's). The window is kept as its
// edge - the least member inside - and moving the edge flips only the members between the old
// and the new one: O(log n + flips) per tick, whatever the stream holds.
import type { Literal, Term } from '@rdfjs/types'
import { NS } from '../ns.ts'
import type { Delta } from '../rdf.ts'
import type { Factory, GraphStore } from '../store/types.ts'
import { OrderedMultiset } from './ordered.ts'
import type { WindowSpec } from './plan.ts'

const TREE_MEMBER = 'https://w3id.org/tree#member'
const XSD = NS.xsd

interface Point {
  t: number
  m: string
}

const cmp = (a: Point, b: Point) => (a.t < b.t ? -1 : a.t > b.t ? 1 : a.m < b.m ? -1 : a.m > b.m ? 1 : 0)

/** A timestamp literal as milliseconds: a number as it is (net:startedAt), a date by its time. */
export function timeOf(t: Term | undefined): number | undefined {
  if (!t || t.termType !== 'Literal') return undefined
  const n = t.datatype.value === XSD + 'dateTime' || t.datatype.value === XSD + 'date' ? Date.parse(t.value) : Number(t.value)
  return Number.isNaN(n) ? undefined : n
}

/** A time as a literal of a timestamp's datatype (dates as UTC, numbers as they are). */
export function timeLiteral(f: Factory, ms: number, datatype: string): Literal {
  // Canonical, as stores write it back: no fraction of a second when it is zero, no trailing zeros.
  if (datatype === XSD + 'dateTime' || datatype === XSD + 'date') return f.literal(new Date(ms).toISOString().replace(/\.?0+Z$/, 'Z'), f.namedNode(XSD + 'dateTime'))
  const integral = datatype === XSD + 'integer' || datatype === XSD + 'int' || datatype === XSD + 'long'
  return f.literal(String(ms), f.namedNode(integral && Number.isInteger(ms) ? datatype : XSD + 'double'))
}

/** The start of the (epoch-aligned) bucket a time falls in. */
export const bucketStart = (ms: number, size: number) => Math.floor(ms / size) * size

export class WindowState {
  private index = new OrderedMultiset<Point>(cmp)
  /** Every indexed member's timestamp (and its literal's datatype, for bucket terms). */
  private times = new Map<string, { t: number; dt: string }>()
  private inside = new Set<string>()
  /** The least point inside (undefined: everything is). */
  private edge?: Point
  /** Members added or moved since the last reconcile, and members that left the index. */
  private pending = new Set<string>()
  private dropped = new Set<string>()
  /** Members whose timestamp changed (their bucket, a column, changes with it). */
  retimed = new Set<string>()
  /** Members that arrived behind the watermark (event time). */
  late = 0

  constructor(
    readonly spec: WindowSpec,
    private readonly store: GraphStore,
    private readonly graphs: () => Term[],
  ) {}

  get size(): number {
    return this.times.size
  }

  get insideCount(): number {
    return this.inside.size
  }

  isIn(member: string): boolean {
    return this.inside.has(member)
  }

  /** Every member of the stream now (start, rebuild). */
  load(): void {
    this.clear()
    const f = this.store.factory
    const seen = new Set<string>()
    for (const g of this.graphs())
      for (const q of this.store.match(f.namedNode(this.spec.stream), f.namedNode(TREE_MEMBER), null, g as never)) {
        if (q.object.termType !== 'NamedNode' || seen.has(q.object.value)) continue
        seen.add(q.object.value)
        this.place(q.object.value, false)
      }
  }

  /** This tick's change to the stream's membership and timestamps; whether the index changed. */
  observe(changes: Delta): boolean {
    const touched = new Set<string>()
    for (const q of [...changes.remove, ...changes.add]) {
      if (q.predicate.value === TREE_MEMBER && q.subject.value === this.spec.stream && q.object.termType === 'NamedNode') touched.add(q.object.value)
      else if (q.predicate.value === this.spec.timeBy && q.subject.termType === 'NamedNode') touched.add(q.subject.value)
    }
    let changed = false
    for (const m of touched) if (this.place(m, true)) changed = true
    return changed
  }

  /** Put a member where its timestamp now says (or take it out); whether anything moved. */
  private place(m: string, live: boolean): boolean {
    const f = this.store.factory
    const nm = f.namedNode(m)
    let member = false
    let lit: Term | undefined
    for (const g of this.graphs()) {
      if (!member && this.store.match(f.namedNode(this.spec.stream), f.namedNode(TREE_MEMBER), nm, g as never).length) member = true
      lit ??= this.store.match(nm, f.namedNode(this.spec.timeBy), null, g as never)[0]?.object
    }
    const t = member ? timeOf(lit) : undefined
    const old = this.times.get(m)
    if (old && t === old.t) return false
    if (!old && t === undefined) return false
    const before = this.watermark()
    if (old) {
      this.index.add({ t: old.t, m }, -1)
      this.times.delete(m)
      this.retimed.add(m)
    }
    if (t === undefined) {
      this.pending.delete(m)
      this.dropped.add(m)
      return true
    }
    this.times.set(m, { t, dt: (lit as Literal).datatype.value })
    this.index.add({ t, m }, 1)
    this.dropped.delete(m)
    this.pending.add(m)
    if (live && !old && this.spec.eventTime && t < before) this.late++
    return true
  }

  /** The greatest timestamp minus the lateness (-Infinity with no members). */
  watermark(): number {
    const last = this.index.size ? this.index.at(this.index.size - 1)!.value.t : -Infinity
    return last - (this.spec.latenessMs ?? 0)
  }

  /** The window's "now": the clock, or with event time the watermark. */
  now(clock: number): number {
    return this.spec.eventTime ? this.watermark() : clock
  }

  /** The least point inside at `clock` (undefined: every member is inside). */
  private edgeAt(clock: number): Point | undefined {
    const s = this.spec
    if (s.kind === 'count') {
      const n = s.amount ?? 0
      return this.index.size > n ? this.index.at(this.index.size - n)!.value : n === 0 ? { t: Infinity, m: '' } : undefined
    }
    const now = this.now(clock)
    if (now === -Infinity) return undefined
    if (s.kind === 'sliding') return { t: now - s.rangeMs!, m: '' }
    return { t: bucketStart(now, s.sizeMs!) - ((s.keep ?? 1) - 1) * s.sizeMs!, m: '' }
  }

  private rankOf(p: Point | undefined): number {
    return p ? this.index.rank(p) : 0
  }

  /** Whether reconciling at `clock` would flip a member (a timer may be late; a tick may come first). */
  due(clock: number): boolean {
    if (this.pending.size || this.dropped.size) return true
    const next = this.edgeAt(clock)
    return this.rankOf(next) !== this.rankOf(this.edge)
  }

  /** Move the edge to `clock`'s; returns the members whose side changed. */
  reconcile(clock: number): Set<string> {
    const next = this.edgeAt(clock)
    const flips = new Set<string>()
    const set = (m: string, inside: boolean) => {
      if (inside === this.inside.has(m)) return
      if (inside) this.inside.add(m)
      else this.inside.delete(m)
      flips.add(m)
    }
    const inside = (p: Point) => !next || cmp(p, next) >= 0
    for (const m of this.dropped) set(m, false)
    this.dropped.clear()
    const a = this.rankOf(this.edge)
    const b = this.rankOf(next)
    for (const { value } of this.index.range(Math.min(a, b), Math.max(a, b))) set(value.m, inside(value))
    for (const m of this.pending) {
      const e = this.times.get(m)
      if (e) set(m, inside({ t: e.t, m }))
    }
    this.pending.clear()
    this.retimed.clear()
    this.edge = next
    return flips
  }

  /** The bucket start of a member, as a term of its timestamp's datatype (tumbling windows). */
  bucket(member: string): Term | undefined {
    const e = this.times.get(member)
    if (!e || this.spec.kind !== 'tumbling') return undefined
    return timeLiteral(this.store.factory, bucketStart(e.t, this.spec.sizeMs!), e.dt)
  }

  /** When the wall clock next moves a member out (undefined: never by itself). */
  nextExpiry(): number | undefined {
    const s = this.spec
    if (s.kind === 'count' || s.eventTime || !this.index.size) return undefined
    const first = this.index.at(this.rankOf(this.edge))?.value
    if (!first) return undefined
    // In while t >= now - range: out one millisecond after t + range. A bucket leaves when `keep` newer ones began.
    return s.kind === 'sliding' ? first.t + s.rangeMs! + 1 : bucketStart(first.t, s.sizeMs!) + (s.keep ?? 1) * s.sizeMs!
  }

  clear(): void {
    this.index.clear()
    this.times.clear()
    this.inside.clear()
    this.pending.clear()
    this.dropped.clear()
    this.retimed.clear()
    this.edge = undefined
  }

  /** What the window says about itself, as the plan's algebra. */
  static algebra(s: WindowSpec): string {
    const clock = s.eventTime ? `event time${s.latenessMs ? `, lateness ${s.latenessMs} ms` : ''}` : 'wall clock'
    if (s.kind === 'count') return `CountWindow(latest ${s.amount} by <${s.timeBy}>)`
    if (s.kind === 'tumbling') return `TumblingWindow(${s.sizeMs} ms, keep ${s.keep ?? 1}${s.bucketVar ? `, ?${s.bucketVar}` : ''} on <${s.timeBy}>, ${clock})`
    return `SlidingWindow(${s.rangeMs} ms on <${s.timeBy}>, ${clock})`
  }
}

