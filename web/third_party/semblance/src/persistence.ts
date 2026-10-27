// Persistence (ontology/layers/persistence.ttl, PERSISTENCE.md): policies in
// the graph say what to remember, where, and whether; this host effect
// restores them before the first projection and writes them back in idle
// time after input changes they cover. Writes, restores and forgets are
// members of per:StateLog, in the persistence graph projections read.
//
// A policy marked iv:strategy iv:Incremental keeps its CONSTRUCT as a maintained
// view (docs/IVM.md, phase 3): the view's changes say when it is dirty (exactly
// what its patterns read), and a write stores only what changed since the last
// one - a chunk of its own beside the stored base, compacted into the base when
// the chunks outgrow it - so writing costs the change, not the policy's size. A
// view reads the input graphs only (views step before projections), so a policy
// opts in when that is all it reads.
import type { Quad, Term } from '@rdfjs/types'
import { Parser, Writer } from 'n3'
import type { ReactionFn } from './behaviour/events.ts'
import type { FnRule } from './engine/context.ts'
import { BEH, EVENTS_GRAPH, MOD, NS, PERSIST_GRAPH, RDF_TYPE, XSD_BOOLEAN, XSD_INTEGER } from './ns.ts'
import { prefixesFor } from './prefixes.ts'
import { type Delta, mint, parseDuration, quadKey } from './rdf.ts'
import type { ViewHandle } from './view/runtime.ts'
import type { Runtime } from './runtime.ts'
import type { GraphStore } from './store/types.ts'
import { labelOf, named, str } from './components/util.ts'

const PER = NS.per
const PROV = NS.prov
const TREE_MEMBER = NS.tree + 'member'
const XSD_DATETIME = NS.xsd + 'dateTime'
/** A term's kind and value, so an IRI and a blank node of the same label stay apart. */
const termKey = (t: Term) => t.termType + ':' + t.value

/** Where a scope keeps its strings. Asynchronous, so IndexedDB fits too. */
export interface Backend {
  get(key: string): Promise<string | undefined>
  set(key: string, value: string): Promise<void>
  remove(key: string): Promise<void>
}

export function memoryBackend(): Backend {
  const m = new Map<string, string>()
  return { get: async (k) => m.get(k), set: async (k, v) => void m.set(k, v), remove: async (k) => void m.delete(k) }
}

export function webStorageBackend(storage: Storage): Backend {
  return {
    get: async (k) => storage.getItem(k) ?? undefined,
    set: async (k, v) => storage.setItem(k, v),
    remove: async (k) => storage.removeItem(k),
  }
}

export function indexedDbBackend(name = 'semblance'): Backend {
  const db = new Promise<IDBDatabase>((resolve, reject) => {
    const req = indexedDB.open(name, 1)
    req.onupgradeneeded = () => req.result.createObjectStore('state')
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
  const tx = async <T>(mode: IDBTransactionMode, run: (s: IDBObjectStore) => IDBRequest<T>) => {
    const d = await db
    return new Promise<T>((resolve, reject) => {
      const r = run(d.transaction('state', mode).objectStore('state'))
      r.onsuccess = () => resolve(r.result)
      r.onerror = () => reject(r.error)
    })
  }
  return {
    get: async (k) => (await tx<string | undefined>('readonly', (s) => s.get(k) as IDBRequest<string | undefined>)) ?? undefined,
    set: async (k, v) => void (await tx('readwrite', (s) => s.put(v, k))),
    remove: async (k) => void (await tx('readwrite', (s) => s.delete(k))),
  }
}

interface Policy {
  iri: string
  scope: string
  enabled: boolean
  version: string
  graph?: string
  retentionMs?: number
  subject?: Term
  properties: string[]
  construct?: string
  /** iv:strategy iv:Incremental: kept as a view (it reads input graphs only). */
  incremental: boolean
  /** Predicates whose changes make the policy dirty. */
  watches: Set<string>
  /** A storage-carried nav:Dimension rather than a declared policy (no switch of its own). */
  dimension?: true
}

interface Blob {
  version: string
  savedAt: number
  /** The base: every triple stored at the last full write. */
  nt: string
  /** Changes written since, each its own entry (`<key>#<i>`, i from 1), applied in order. */
  chunks?: number
}

/** A change written on its own (see Blob.chunks). */
interface Chunk {
  add: string
  remove: string
}

/** A policy kept by a view: what was last written (its tick), and the chunks and triples since the base. */
interface Kept {
  view: ViewHandle
  tick: number
  chunks: number
  chunkTriples: number
  baseTriples: number
  stop: () => void
}

/** The persistence effect for one store. */
export class Persistence {
  private runtime?: Runtime
  private dirty = new Set<string>()
  private timer?: ReturnType<typeof setTimeout>
  private seq = 0
  private log: Term[] = []
  private restoredCounts = new Map<string, number>()
  /** CONSTRUCT policies maintained as views, by policy IRI. */
  private kept = new Map<string, Kept>()
  /** What writes cost (tests): entries set, and the triples in them. */
  readonly writes = { entries: 0, triples: 0 }

  constructor(
    readonly store: GraphStore,
    readonly backends: Record<string, Backend>,
    readonly opts: { prefix?: string; delayMs?: number } = {},
  ) {}

  private key(policy: string) {
    return (this.opts.prefix ?? 'semblance:') + policy
  }
  private backend(scope: string) {
    return this.backends[scope] ?? this.backends[PER + 'Session']!
  }

  private cachedPolicies?: Policy[]

  /** The policies declared in the graph (cached until a change touches per: terms). */
  policies(): Policy[] {
    return (this.cachedPolicies ??= this.readPolicies())
  }

  private readPolicies(): Policy[] {
    const s = this.store
    const f = s.factory
    const nn = (x: string) => f.namedNode(x)
    const one = (subject: Term, p: string) => s.match(subject, nn(p), null)[0]?.object
    const policies = s.match(null, nn(RDF_TYPE), nn(PER + 'Policy')).map(({ subject: p }) => {
      const construct = one(p, PER + 'construct')?.value
      const listHead = one(p, PER + 'properties')
      const properties: string[] = []
      for (let n = listHead; n && n.value !== NS.rdf + 'nil'; n = one(n, NS.rdf + 'rest')) {
        const first = one(n, NS.rdf + 'first')
        if (first) properties.push(first.value)
      }
      const duration = one(p, PER + 'retention')?.value
      return {
        iri: p.value,
        scope: one(p, PER + 'scope')?.value ?? PER + 'Session',
        enabled: one(p, PER + 'enabled')?.value !== 'false',
        version: one(p, PER + 'version')?.value ?? '1',
        graph: one(p, PER + 'graph')?.value,
        retentionMs: duration ? parseDuration(duration) : undefined,
        subject: one(p, PER + 'subject'),
        properties,
        construct,
        incremental: one(p, NS.iv + 'strategy')?.value === NS.iv + 'Incremental',
        watches: new Set([...properties, ...(construct ? this.mentioned(construct) : [])]),
      }
    })
    // A dimension of a location (nav:Dimension) carried by a storage scope is remembered like a
    // policy over its subject and property: storage has one writer.
    const dimensions = s.match(null, nn(NS.nav + 'carriedBy'), null).flatMap(({ subject: d, object: scope }): Policy[] => {
      const subject = one(d, NS.nav + 'subject')
      const property = one(d, NS.nav + 'property')?.value
      if (!subject || !property || !s.match(scope, nn(RDF_TYPE), nn(PER + 'Scope')).length) return []
      return [{ iri: d.value, scope: scope.value, enabled: true, version: '1', subject, properties: [property], incremental: false, watches: new Set([property]), dimension: true }]
    })
    return [...policies, ...dimensions]
  }

  /** IRIs a query mentions (full or prefixed), for deciding what changes concern it. */
  private mentioned(query: string): string[] {
    const prefixes = { ...Object.fromEntries(Object.entries(NS)), ...this.declaredPrefixes() }
    const out = [...query.matchAll(/<([^>\s]+)>/g)].map((m) => m[1]!)
    for (const m of query.matchAll(/\b([a-z][\w-]*):([A-Za-z_][\w-]*)/g)) if (prefixes[m[1]!]) out.push(prefixes[m[1]!] + m[2])
    return out
  }
  private declaredPrefixes(): Record<string, string> {
    const out: Record<string, string> = {}
    for (const line of prefixesFor(this.store).split('\n')) {
      const m = /^PREFIX (\S*): <([^>]*)>/.exec(line)
      if (m) out[m[1]!] = m[2]!
    }
    return out
  }

  /**
   * Put enabled policies' stored triples back. Call before projecting, so the
   * first render already shows them. Returns how many triples were restored.
   */
  async restore(): Promise<number> {
    const f = this.store.factory
    // The switches themselves are remembered on the device.
    const flags = await this.backend(PER + 'Device').get(this.key('enabled'))
    if (flags) {
      for (const [iri, on] of Object.entries(JSON.parse(flags) as Record<string, boolean>)) {
        const p = f.namedNode(iri)
        for (const q of this.store.match(p, f.namedNode(PER + 'enabled'), null)) this.store.delete(q)
        this.store.add(f.quad(p, f.namedNode(PER + 'enabled'), f.literal(String(on), f.namedNode(XSD_BOOLEAN))))
      }
    }
    let total = 0
    for (const policy of this.policies()) {
      if (!policy.enabled) continue
      const raw = await this.backend(policy.scope).get(this.key(policy.iri))
      if (!raw) continue
      let blob: Blob
      try {
        blob = JSON.parse(raw)
      } catch {
        continue
      }
      const expired = policy.retentionMs !== undefined && Date.now() - blob.savedAt > policy.retentionMs
      if (blob.version !== policy.version || expired) {
        await this.removeStored(policy)
        continue
      }
      const g = policy.graph ? f.namedNode(policy.graph) : f.defaultGraph()
      const held = new Map(this.parse(blob.nt).map((q) => [quadKey(q), q]))
      for (let i = 1; i <= (blob.chunks ?? 0); i++) {
        const raw = await this.backend(policy.scope).get(this.key(policy.iri) + '#' + i)
        if (!raw) break
        const chunk = JSON.parse(raw) as Chunk
        for (const q of this.parse(chunk.remove)) held.delete(quadKey(q))
        for (const q of this.parse(chunk.add)) held.set(quadKey(q), q)
      }
      const quads = [...held.values()].map((q) => f.quad(q.subject, q.predicate, q.object, g))
      // What is restored replaces the current values of the same subject and property.
      // Keyed by the terms themselves (a remembered subject may be a blank node, not an IRI).
      const pairs = new Map(quads.map((q) => [termKey(q.subject) + ' ' + termKey(q.predicate), q] as const))
      if (!policy.graph) {
        for (const { subject, predicate } of pairs.values()) for (const q of this.store.match(subject, predicate, null, g)) this.store.delete(q)
      }
      for (const q of quads) if (!this.store.has(q)) this.store.add(q)
      this.restoredCounts.set(policy.iri, quads.length)
      total += quads.length
    }
    return total
  }

  /** Whether the last restore put back what a policy (or a storage-carried dimension) stored. */
  restored(policy: string): boolean {
    return (this.restoredCounts.get(policy) ?? 0) > 0
  }

  /** Follow input changes from now on; report restores in the state log. */
  attach(runtime: Runtime): () => void {
    this.runtime = runtime
    for (const [policy, n] of this.restoredCounts) this.record(PER + 'Restore', this.policies().find((p) => p.iri === policy)!, n)
    for (const policy of this.policies()) this.report(policy)
    // Where we are may have come from the address since: storage-carried dimensions are written once.
    for (const policy of this.policies()) if (policy.dimension) this.dirty.add(policy.iri)
    if (this.dirty.size) this.schedule()
    this.keep()
    const stop = runtime.onInput((graph, delta) => this.changed(graph, delta))
    return () => {
      stop()
      for (const [iri, k] of this.kept) k.stop(), runtime.views.unhost(mint(iri, 'view'))
      this.kept.clear()
    }
  }

  /**
   * Each CONSTRUCT policy marked iv:strategy iv:Incremental as a view, checked against a run of its
   * query (a policy that reads a projection's derived graph after all stays a run at each write).
   */
  private keep() {
    const runtime = this.runtime
    if (!runtime || !this.store.sparql) return
    for (const policy of this.policies()) {
      if (!policy.construct || !policy.incremental || policy.subject || this.kept.has(policy.iri)) continue
      const iri = mint(policy.iri, 'view')
      const view = runtime.views.host(iri, policy.construct)
      if (!view) continue
      const ours = new Set(view.quads().map(quadKey))
      const theirs = this.collect(policy).map(quadKey)
      const sensitive = new Set(view.quads().filter((q) => this.isSensitive(q.predicate)).map(quadKey))
      if (theirs.length !== ours.size - sensitive.size || theirs.some((k) => !ours.has(k))) {
        runtime.views.unhost(iri)
        continue
      }
      view.keepHistory(64)
      const stop = view.subscribe(() => {
        if (!policy.enabled) return
        this.dirty.add(policy.iri)
        this.schedule()
      })
      this.kept.set(policy.iri, { view, tick: -1, chunks: 0, chunkTriples: 0, baseTriples: 0, stop })
    }
  }

  private changed(graph: string | undefined, delta: Delta) {
    if (graph === PERSIST_GRAPH) return
    if (graph === undefined && [...delta.add, ...delta.remove].some((q) => q.predicate.value.startsWith(PER) || q.predicate.value.startsWith(NS.nav) || q.object.value === PER + 'Policy')) {
      this.cachedPolicies = undefined
      // A policy's view follows its query (and its switch: a kept policy that is turned on is written whole).
      for (const [iri, k] of [...this.kept]) {
        const p = this.policies().find((x) => x.iri === iri)
        if (p && p.construct && p.incremental) continue
        k.stop()
        this.runtime?.views.unhost(mint(iri, 'view'))
        this.kept.delete(iri)
      }
      this.keep()
    }
    const policies = this.policies()
    for (const q of [...delta.add, ...delta.remove]) {
      const p = q.predicate.value
      if (p === PER + 'enabled' || p === PER + 'request') {
        const policy = policies.find((x) => x.iri === q.subject.value)
        if (policy) void this.control(policy, delta)
        continue
      }
      for (const policy of policies) {
        if (!policy.enabled || this.dirty.has(policy.iri) || this.kept.has(policy.iri)) continue
        const covers = policy.graph
          ? graph === policy.graph
          : graph === undefined && (policy.subject ? q.subject.equals(policy.subject) && policy.watches.has(p) : policy.watches.has(p))
        if (covers) this.dirty.add(policy.iri)
      }
    }
    if (this.dirty.size) this.schedule()
  }

  private schedule() {
    if (this.timer) return
    this.timer = setTimeout(() => {
      this.timer = undefined
      void this.flush()
    }, this.opts.delayMs ?? 250)
  }

  /** A switch or a request on a policy: remember the switch, forget when asked or turned off. */
  private async control(policy: Policy, delta: Delta) {
    const f = this.store.factory
    const flags: Record<string, boolean> = {}
    for (const p of this.policies()) if (!p.dimension) flags[p.iri] = p.enabled
    await this.backend(PER + 'Device').set(this.key('enabled'), JSON.stringify(flags))
    const forgetting = delta.add.some((q) => q.subject.value === policy.iri && q.predicate.value === PER + 'request' && q.object.value === 'forget')
    if (!policy.enabled || forgetting) {
      await this.removeStored(policy)
      const k = this.kept.get(policy.iri)
      if (k) k.tick = -1
      this.record(PER + 'Forget', policy, 0)
      this.report(policy, 0)
    } else if (delta.add.some((q) => q.subject.value === policy.iri && q.predicate.value === PER + 'enabled')) {
      this.dirty.add(policy.iri)
      this.schedule()
    }
    if (forgetting) {
      const requests = this.store.match(f.namedNode(policy.iri), f.namedNode(PER + 'request'), null, f.defaultGraph())
      if (requests.length) this.runtime?.apply({ remove: requests, add: [] })
    }
  }

  /** Write every dirty policy now. */
  async flush(): Promise<void> {
    if (this.timer) clearTimeout(this.timer), (this.timer = undefined)
    const dirty = [...this.dirty]
    this.dirty.clear()
    for (const policy of this.policies()) {
      if (!dirty.includes(policy.iri) || !policy.enabled) continue
      const k = this.kept.get(policy.iri)
      if (k && (await this.writeChange(policy, k))) continue
      // The tick the triples are of: what changes after it is the next write's.
      const tick = this.runtime?.views.tickNo ?? -1
      const quads = k ? k.view.quads().filter((q) => !this.isSensitive(q.predicate)) : this.collect(policy)
      await this.removeStored(policy)
      const blob: Blob = { version: policy.version, savedAt: Date.now(), nt: this.serialize(quads) }
      await this.set(policy, this.key(policy.iri), JSON.stringify(blob), quads.length)
      if (k) Object.assign(k, { tick, chunks: 0, chunkTriples: 0, baseTriples: quads.length })
      this.record(PER + 'Write', policy, quads.length)
      this.report(policy, quads.length)
    }
  }

  /**
   * A kept policy's change since its last write, as one more chunk; false when it cannot be (nothing
   * written yet, the view no longer reaches back, or the chunks outgrew the base: write it whole).
   */
  private async writeChange(policy: Policy, k: Kept): Promise<boolean> {
    if (k.tick < 0) return false
    const change = k.view.since(k.tick)
    if (!change) return false
    const add: Quad[] = []
    const remove: Quad[] = []
    for (const [, q, w] of change.quads.entries()) if (!this.isSensitive(q.predicate)) (w > 0 ? add : remove).push(q)
    const n = add.length + remove.length
    if (k.chunks >= 64 || k.chunkTriples + n > Math.max(16, k.baseTriples)) return false
    k.tick = change.tick
    if (!n) return true
    const raw = await this.backend(policy.scope).get(this.key(policy.iri))
    if (!raw) return false
    const blob = JSON.parse(raw) as Blob
    k.chunks++
    k.chunkTriples += n
    await this.set(policy, this.key(policy.iri) + '#' + k.chunks, JSON.stringify({ add: this.serialize(add), remove: this.serialize(remove) } satisfies Chunk), n)
    await this.set(policy, this.key(policy.iri), JSON.stringify({ ...blob, savedAt: Date.now(), chunks: k.chunks } satisfies Blob), 0)
    const size = k.view.quads().filter((q) => !this.isSensitive(q.predicate)).length
    this.record(PER + 'Write', policy, n)
    this.report(policy, size)
    return true
  }

  private async set(policy: Policy, key: string, value: string, triples: number) {
    this.writes.entries++
    this.writes.triples += triples
    await this.backend(policy.scope).set(key, value)
  }

  /** Remove what a policy stored: the base and its chunks. */
  private async removeStored(policy: Policy) {
    const b = this.backend(policy.scope)
    const raw = await b.get(this.key(policy.iri))
    let chunks = 0
    try {
      chunks = raw ? ((JSON.parse(raw) as Blob).chunks ?? 0) : 0
    } catch {
      chunks = 0
    }
    for (let i = 1; i <= chunks; i++) await b.remove(this.key(policy.iri) + '#' + i)
    await b.remove(this.key(policy.iri))
  }

  private isSensitive(p: Term): boolean {
    return this.store.match(p, this.store.factory.namedNode(PER + 'sensitive'), null).some((q) => q.object.value === 'true')
  }

  /** The triples a policy covers right now (sensitive properties left out). */
  collect(policy: Policy): Quad[] {
    const s = this.store
    const f = s.factory
    const sensitive = (p: Term) => s.match(p, f.namedNode(PER + 'sensitive'), null).some((q) => q.object.value === 'true')
    let quads: Quad[] = []
    if (policy.subject) {
      for (const p of policy.properties) quads.push(...s.match(policy.subject, f.namedNode(p), null, f.defaultGraph()))
    } else if (policy.construct && s.sparql) {
      const graphs = [f.defaultGraph(), f.namedNode(EVENTS_GRAPH), ...(this.runtime?.projections.map((p) => p.engine.graph) ?? [])]
      if (policy.graph) graphs.push(f.namedNode(policy.graph))
      quads = s.construct(prefixesFor(s) + '\n' + policy.construct, graphs)
    }
    return quads.filter((q) => !sensitive(q.predicate))
  }

  private serialize(quads: Quad[]): string {
    const w = new Writer({ format: 'N-Triples' })
    for (const q of quads) w.addQuad(q.subject as never, q.predicate as never, q.object as never)
    let out = ''
    w.end((_e, r) => (out = r ?? ""))
    return out
  }

  private parse(nt: string): Quad[] {
    const f = this.store.factory
    const term = (t: Term): Term =>
      t.termType === 'NamedNode' ? f.namedNode(t.value) : t.termType === 'Literal' ? f.literal(t.value, t.language || f.namedNode(t.datatype.value)) : t
    return new Parser({ format: 'N-Triples' }).parse(nt).map((q) => f.quad(term(q.subject as Term), term(q.predicate as Term), term(q.object as Term)))
  }

  /** A member of per:StateLog (bounded to 100). */
  private record(kind: string, policy: Policy, size: number) {
    const runtime = this.runtime
    if (!runtime) return
    const f = this.store.factory
    const nn = (x: string) => f.namedNode(x)
    const m = nn(`urn:semblance:state:${Date.now().toString(36)}-${++this.seq}`)
    const stream = nn(PER + 'StateLog')
    const add = [
      f.quad(m, nn(RDF_TYPE), nn(kind)),
      f.quad(m, nn(PER + 'policy'), nn(policy.iri)),
      f.quad(m, nn(PER + 'scope'), nn(policy.scope)),
      f.quad(m, nn(PER + 'size'), f.literal(String(size), nn(XSD_INTEGER))),
      f.quad(m, nn(PROV + 'generatedAtTime'), f.literal(new Date().toISOString(), nn(XSD_DATETIME))),
      f.quad(stream, nn(TREE_MEMBER), m),
    ]
    // Which interaction led to it: the newest member of the change log.
    const cause = this.store.match(nn(BEH.ChangeLog), nn(BEH.latest), null)[0]?.object
    if (cause && kind === PER + 'Write') add.push(f.quad(m, nn(PROV + 'wasInformedBy'), cause))
    this.log.push(m)
    const remove: Quad[] = []
    while (this.log.length > 100) {
      const old = this.log.shift()!
      remove.push(...this.store.match(old, null, null, nn(PERSIST_GRAPH)), ...this.store.match(stream, nn(TREE_MEMBER), old, nn(PERSIST_GRAPH)))
    }
    runtime.write(PERSIST_GRAPH, { add, remove })
  }

  /** The policy's current size and last write, for c:PolicyList. */
  private report(policy: Policy, size?: number) {
    const runtime = this.runtime
    if (!runtime) return
    const f = this.store.factory
    const nn = (x: string) => f.namedNode(x)
    const p = nn(policy.iri)
    const g = nn(PERSIST_GRAPH)
    const remove = [PER + 'storedSize', PER + 'lastWritten', PER + 'restored'].flatMap((x) => this.store.match(p, nn(x), null, g))
    const add: Quad[] = []
    if (size !== undefined) {
      add.push(f.quad(p, nn(PER + 'storedSize'), f.literal(String(size), nn(XSD_INTEGER))))
      if (size > 0) add.push(f.quad(p, nn(PER + 'lastWritten'), f.literal(new Date().toISOString(), nn(XSD_DATETIME))))
    }
    const restored = this.restoredCounts.get(policy.iri)
    if (restored !== undefined) add.push(f.quad(p, nn(PER + 'restored'), f.literal(String(restored), nn(XSD_INTEGER))))
    if (!add.length && !remove.length) return
    runtime.write(PERSIST_GRAPH, { remove, add })
  }
}

// --- c:PolicyList ------------------------------------------------------------------------------

const policyList: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const policies = ctx
    .match(null, RDF_TYPE, PER + 'Policy')
    .map((q) => q.subject)
    .filter(named)
    .sort((a, b) => labelOf(ctx, a).localeCompare(labelOf(ctx, b)))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, policies.map((p, i) => o.text('l' + i, `[${ctx.object(p, PER + 'enabled')?.value === 'false' ? ' ' : 'x'}] ${labelOf(ctx, p)}\n`))))
    return o.quads
  }
  const rows = policies.map((p, i) => {
    const on = ctx.object(p, PER + 'enabled')?.value !== 'false'
    const scope = ctx.object(p, PER + 'scope')
    const size = Number(str(ctx.object(p, PER + 'storedSize')) ?? 0)
    const restored = str(ctx.object(p, PER + 'restored'))
    const when = str(ctx.object(p, PER + 'lastWritten'))
    const k = 'p' + i
    const toggle = o.el(k + '-switch', 'Button', { type: 'button', role: 'switch', 'aria-checked': String(on), 'aria-label': labelOf(ctx, p) }, [o.el(k + '-thumb', 'Span', { 'aria-hidden': 'true' })])
    o.t(toggle, PER + 'toggles', p)
    const forget = o.el(k + '-forget', 'Button', { type: 'button', class: 'sem-forget', disabled: size > 0 ? undefined : '' }, [o.text(k + '-forget-text', '⌫')])
    o.attr(forget, 'aria-label', `Forget ${labelOf(ctx, p)}`)
    o.t(forget, PER + 'forgets', p)
    const status = [
      scope ? labelOf(ctx, scope) : '',
      on ? (size ? `${size} triples` : 'nothing stored yet') : 'off',
      restored ? `${restored} restored at load` : '',
      when ? `saved ${when.slice(11, 19)}` : '',
    ].filter(Boolean)
    return o.el(k, 'Li', {}, [
      toggle,
      o.el(k + '-what', 'Div', { class: 'sem-what' }, [
        o.el(k + '-label', 'Strong', {}, [o.text(k + '-label-text', labelOf(ctx, p))]),
        o.el(k + '-status', 'Span', {}, [o.text(k + '-status-text', status.join(' · '))]),
      ]),
      forget,
    ])
  })
  o.renders(o.el('root', 'Ul', { 'aria-label': labelOf(ctx, ctx.nn(PER + 'Policy')) }, rows))
  return o.quads
}

/** The policy a click hit, through the marker on the rendered element. */
function hitPolicy(ctx: Parameters<ReactionFn>[0], marker: string): Term | undefined {
  for (let n = ctx.object(ctx.event, BEH.node), hops = 0; n && hops < 32; hops++, n = ctx.match(null, NS.rdfhtml + 'hasChild', n)[0]?.subject) {
    const p = ctx.object(n, marker)
    if (p) return p
  }
  return undefined
}

const togglePolicy: ReactionFn = (ctx) => {
  const p = hitPolicy(ctx, PER + 'toggles')
  if (!p) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  const current = ctx.match(p, PER + 'enabled', null)
  const on = !current.some((q) => q.object.value === 'false')
  return { remove: current, add: [f.quad(p as never, f.namedNode(PER + 'enabled'), f.literal(String(!on), f.namedNode(XSD_BOOLEAN)))] }
}

const forgetPolicy: ReactionFn = (ctx) => {
  const p = hitPolicy(ctx, PER + 'forgets')
  if (!p) return { add: [], remove: [] }
  const f = ctx.runtime.store.factory
  return { remove: [], add: [f.quad(p as never, f.namedNode(PER + 'request'), f.literal('forget'))] }
}

export const persistenceFunctions: Record<string, FnRule> = { 'policy-list': policyList }
export const persistenceReactions: Record<string, ReactionFn> = { 'persistence:toggle': togglePolicy, 'persistence:forget': forgetPolicy }
