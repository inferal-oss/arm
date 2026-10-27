// Replicating a maintained view (docs/IVM.md §16): its changes, tick by tick, as messages another
// store applies - another tab (a BroadcastChannel), a worker, a server - so the replica holds what
// the view holds without evaluating anything. A replica that joins late, or falls behind further
// than the view keeps its changes (ViewHandle.since), gets the whole result once, marked reset.
//
// Messages are plain data (N-Triples terms), so any channel that carries JSON carries them:
//
//   { view, tick, reset?, add: ["<s> <p> <o> ."…], remove: […], rows?: { add: [{ v: "<term>" }…], remove: […] } }
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import { Parser } from 'n3'
import { quadKey } from '../rdf.ts'
import type { GraphStore } from '../store/types.ts'
import { sparqlTerm } from './compile.ts'
import { mapKey, type ViewChange, type ViewRuntime } from './runtime.ts'
import { ZSet } from './zset.ts'

export interface ViewMessage {
  view: string
  /** The source's tick this message brings a replica up to, and (unless reset) the tick it follows on from. */
  tick: number
  since?: number
  /** The whole result: drop what was held first. */
  reset?: boolean
  /** Triples (a CONSTRUCT's, a materialized SELECT's rs: rows), once per copy. */
  add: string[]
  remove: string[]
  /** SELECT rows: variable -> term. */
  rows?: { add: Record<string, string>[]; remove: Record<string, string>[] }
}

/** A replica asking to be brought up to date from the tick it has. */
export interface CatchUp {
  view: string
  since?: number
}

const line = (q: Quad) => `${sparqlTerm(q.subject)} ${sparqlTerm(q.predicate)} ${sparqlTerm(q.object)} .`

function message(view: string, change: ViewChange, since: number | undefined): ViewMessage {
  const add: string[] = []
  const remove: string[] = []
  for (const [, q, w] of change.quads.entries()) for (let i = 0; i < Math.abs(w); i++) (w > 0 ? add : remove).push(line(q))
  const rows = { add: [] as Record<string, string>[], remove: [] as Record<string, string>[] }
  for (const [, r, w] of change.rows.entries()) {
    const o = Object.fromEntries([...r].map(([k, t]) => [k, sparqlTerm(t)]))
    for (let i = 0; i < Math.abs(w); i++) (w > 0 ? rows.add : rows.remove).push(o)
  }
  return { view, tick: change.tick, since, reset: since === undefined || undefined, add, remove, rows: rows.add.length || rows.remove.length ? rows : undefined }
}

/**
 * Send a view's changes after every tick that changed it (and the whole result first). Survives
 * the view being compiled again: it is looked up by IRI each tick, and a new one starts with a
 * reset. Returns `answer` (for catch-up requests) and `stop`.
 */
export function publish(rt: ViewRuntime, iri: string, send: (m: ViewMessage) => void) {
  let sent: number | undefined
  let handle: unknown
  const pump = () => {
    const v = rt.view(iri) as ReturnType<ViewRuntime['view']>
    if (!v) return
    v.keepHistory(16)
    const change = handle === v && sent !== undefined ? v.since(sent) : undefined
    if (change) {
      if (change.quads.size || change.rows.size) send(message(iri, change, sent))
    } else send(message(iri, v.snapshot(), undefined))
    handle = v
    sent = rt.tickNo
  }
  const stop = rt.afterTick(pump)
  if (rt.isStarted) pump()
  return {
    stop,
    /** A replica's request: its changes since the tick it has, or the whole result. */
    answer(req: CatchUp): ViewMessage | undefined {
      const v = rt.view(req.view) as ReturnType<ViewRuntime['view']>
      if (!v || req.view !== iri) return undefined
      const change = req.since !== undefined ? v.since(req.since) : undefined
      return change ? message(iri, change, req.since) : message(iri, v.snapshot(), undefined)
    },
  }
}

/** A view's output, held in another store (its own graph there) from a publisher's messages. */
export class Replica {
  /** The source tick this replica is up to (undefined before the first message). */
  tick?: number
  private counts = new Map<string, { q: Quad; n: number }>()
  private held = new ZSet<Map<string, Term>>()

  constructor(
    readonly view: string,
    private readonly store: GraphStore,
    readonly graph: NamedNode = store.factory.namedNode(view),
  ) {}

  /** The request to send when joining, or after missing a message. */
  catchUp(): CatchUp {
    return { view: this.view, since: this.tick }
  }

  /** Apply a message; false when it does not follow from what is held (ask to catch up). */
  apply(m: ViewMessage): boolean {
    if (m.view !== this.view) return true
    if (!m.reset && (this.tick === undefined || m.since !== this.tick)) return m.since !== undefined && this.tick !== undefined && m.tick <= this.tick
    if (m.reset) this.clear()
    const f = this.store.factory
    const parse = (lines: string[]) => (lines.length ? (new Parser({ format: 'N-Triples', factory: f as never }).parse(lines.join('\n')) as Quad[]) : [])
    const add: Quad[] = []
    const remove: Quad[] = []
    for (const [qs, w] of [[parse(m.remove), -1], [parse(m.add), 1]] as const) {
      for (const q0 of qs) {
        const q = f.quad(q0.subject as NamedNode, q0.predicate as NamedNode, q0.object, this.graph)
        const k = quadKey(q)
        const e = this.counts.get(k)
        const n = (e?.n ?? 0) + w
        if (n < 0) throw new Error(`replica of <${this.view}>: a triple left that it never held (${k})`)
        if (n === 0) this.counts.delete(k), remove.push(e!.q)
        else if (!e) this.counts.set(k, { q, n }), add.push(q)
        else e.n = n
      }
    }
    for (const q of remove) this.store.delete(q)
    for (const q of add) this.store.add(q)
    if (m.rows) {
      const term = (s: string) => (new Parser({ format: 'N-Triples', factory: f as never }).parse(`<urn:x> <urn:y> ${s} .`)[0] as Quad).object
      for (const [rows, w] of [[m.rows.remove, -1], [m.rows.add, 1]] as const)
        for (const o of rows) {
          const r = new Map(Object.entries(o).map(([k, v]) => [k, term(v)]))
          this.held.add(mapKey(r), r, w)
        }
    }
    this.tick = m.tick
    return true
  }

  /** The SELECT rows held, with their multiplicities. */
  rows(): Map<string, Term>[] {
    const out: Map<string, Term>[] = []
    for (const [, r, w] of this.held.entries()) for (let i = 0; i < w; i++) out.push(r)
    return out
  }

  quads(): Quad[] {
    return [...this.counts.values()].map((e) => e.q)
  }

  private clear() {
    for (const { q } of this.counts.values()) this.store.delete(q)
    this.counts.clear()
    this.held.clear()
  }
}

/** Publish a view on a BroadcastChannel (another tab of the same origin replicates it). */
export function broadcast(rt: ViewRuntime, iri: string, name: string) {
  const channel = new BroadcastChannel(name)
  const p = publish(rt, iri, (m) => channel.postMessage(m))
  channel.onmessage = (e: MessageEvent) => {
    const req = e.data as CatchUp | ViewMessage
    if (!('add' in req)) {
      const answer = p.answer(req)
      if (answer) channel.postMessage(answer)
    }
  }
  return () => (p.stop(), channel.close())
}

/** Follow a broadcast view into a store; asks to catch up when it joins or misses a message. */
export function follow(replica: Replica, name: string) {
  const channel = new BroadcastChannel(name)
  channel.onmessage = (e: MessageEvent) => {
    const m = e.data as CatchUp | ViewMessage
    if ('add' in m && !replica.apply(m)) channel.postMessage(replica.catchUp())
  }
  channel.postMessage(replica.catchUp())
  return () => channel.close()
}
