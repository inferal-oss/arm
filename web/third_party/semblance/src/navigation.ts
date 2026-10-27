// Navigation (ontology/layers/navigation.ttl): where we are is graph data. A
// nav:Location groups the dimensions of one mounted app; each dimension says
// which node and property hold it, where it is carried (the URL's fragment or
// query; storage scopes, which the persistence effect writes), whether a change
// makes history, and when it applies. This host effect reads the URL into the
// graph before the first projection, writes the graph back to the URL after input
// changes (coalesced, one history step per change), turns Back, Forward and typed
// URLs into nav:Navigate events, and brings a followed target into view.
import { md5 } from './components/data.ts'
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionFn } from './behaviour/events.ts'
import type { EventBus } from './behaviour/events.ts'
import type { FnRule, RuleContext } from './engine/context.ts'
import { NAV_GRAPH, NS, RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE, SEM, XSD_BOOLEAN, XSD_INTEGER } from './ns.ts'
import type { Delta } from './rdf.ts'
import type { Runtime } from './runtime.ts'
import type { Factory, GraphStore } from './store/types.ts'
import { motionCalmed } from './layers/motion.ts'

const NAV = NS.nav
const C = NS.c
const SH = NS.sh
const RDFS_RANGE = NS.rdfs + 'range'
const URL_CARRIERS = new Set([NAV + 'Fragment', NAV + 'Query'])
const GO_TO = NAV + 'goTo'

// --- the model, read from the graph ----------------------------------------------------------

/** What the model reads through: the store's default graph, or a rule's view. */
interface Reader {
  f: Factory
  match(s: Term | null, p: string | null, o: Term | null): Quad[]
}

export interface Dimension {
  iri: string
  subject: NamedNode
  property: string
  /** Its name in a query string. */
  key: string
  keyProperty: string
  /** Spelled as prefixed names rather than by key. */
  prefixed: boolean
  /** Carrier IRIs, best rank first. */
  carriers: string[]
  makesHistory: boolean
  parent?: string
  when: Term[]
  target: boolean
  /** Its values are terms of the vocabulary (nav:showsTerms): a link to a term no component shows goes here. */
  terms: boolean
}

export interface Location {
  iri: string
  root?: string
  dimensions: Dimension[]
}

const one = (r: Reader, s: Term, p: string) => r.match(s, p, null)[0]?.object
const list = (r: Reader, head: Term | undefined): Term[] => {
  const out: Term[] = []
  for (let n = head; n && n.value !== RDF_NIL && out.length < 1000; n = one(r, n, RDF_REST)) {
    const first = one(r, n, RDF_FIRST)
    if (first) out.push(first)
  }
  return out
}
const localName = (iri: string) => iri.slice(Math.max(iri.lastIndexOf('#'), iri.lastIndexOf('/')) + 1)
const same = (a: Term | undefined, b: Term | undefined) => !!a && !!b && a.termType === b.termType && a.value === b.value

/** The locations declared in the graph, with their dimensions in order. */
export function readLocations(r: Reader): Location[] {
  const rank = (carrier: string) => Number(one(r, r.f.namedNode(carrier), NAV + 'rank')?.value ?? 99)
  const locations = r.match(null, RDF_TYPE, r.f.namedNode(NAV + 'Location')).map(({ subject: loc }) => {
    const dimensions = list(r, one(r, loc, NAV + 'dimensions'))
      .filter((d): d is NamedNode => d.termType === 'NamedNode')
      .flatMap((d): Dimension[] => {
        const subject = one(r, d, NAV + 'subject')
        const property = one(r, d, NAV + 'property')?.value
        if (subject?.termType !== 'NamedNode' || !property) return []
        const types = r.match(d, RDF_TYPE, null).map((q) => q.object.value)
        return [
          {
            iri: d.value,
            subject,
            property,
            key: one(r, d, NAV + 'key')?.value ?? localName(property),
            keyProperty: one(r, d, NAV + 'keyProperty')?.value ?? SEM.slug,
            prefixed: one(r, d, NAV + 'spelling')?.value === NAV + 'PrefixedName',
            carriers: r
              .match(d, NAV + 'carriedBy', null)
              .map((q) => q.object.value)
              .sort((a, b) => rank(a) - rank(b)),
            makesHistory: one(r, d, NAV + 'history')?.value !== NAV + 'NoHistory',
            parent: one(r, d, NAV + 'parent')?.value,
            when: r.match(d, NAV + 'when', null).map((q) => q.object),
            target: types.includes(NAV + 'TargetDimension'),
            terms: one(r, d, NAV + 'showsTerms')?.value === 'true',
          },
        ]
      })
    return { iri: loc.value, root: one(r, loc, NAV + 'root')?.value, dimensions }
  })
  return locations.sort((a, b) => a.iri.localeCompare(b.iri))
}

/** Prefixes for spelling IRIs: the framework's, and those the graph declares (sh:declare). */
function prefixMap(r: Reader): [string, string][] {
  const out: [string, string][] = Object.entries(NS)
  for (const q of r.match(null, SH + 'prefix', null)) {
    const ns = one(r, q.subject, SH + 'namespace')?.value
    if (ns && !out.some(([p]) => p === q.object.value)) out.push([q.object.value, ns])
  }
  // Longest namespace first, so the most specific prefix spells an IRI.
  return out.sort((a, b) => b[1].length - a[1].length)
}

export type Values = Map<string, Term>

/** The location's current values, from the graph. */
export function currentValues(r: Reader, loc: Location): Values {
  const out: Values = new Map()
  for (const d of loc.dimensions) {
    const v = one(r, d.subject, d.property)
    if (v) out.set(d.iri, v)
  }
  return out
}

/** Whether a dimension applies under these values (its parent chain has matching values). */
export function inScope(r: Reader, loc: Location, d: Dimension, values: Values, depth = 0): boolean {
  if (d.target) {
    const v = values.get(d.iri)
    if (!v || v.termType !== 'NamedNode') return false
    const needs = shows(r, loc, v)
    return !!needs && [...needs].every(([dim, val]) => same(values.get(dim), val))
  }
  if (!d.parent) return true
  const parent = loc.dimensions.find((p) => p.iri === d.parent)
  if (!parent || depth > 16) return false
  return d.when.some((w) => same(w, values.get(parent.iri))) && inScope(r, loc, parent, values, depth + 1)
}

// --- spelling values as URL text, and reading them back ----------------------------------------

function spell(r: Reader, d: Dimension, v: Term): string {
  if (v.termType === 'Literal') return v.value
  if (d.target) return one(r, v, NAV + 'anchor')?.value ?? localName(v.value)
  const key = d.prefixed ? undefined : (one(r, v, d.keyProperty) ?? one(r, v, SEM.slug))
  if (key?.termType === 'Literal') return key.value
  for (const [p, ns] of prefixMap(r)) if (v.value.startsWith(ns) && /^[\w-]+$/.test(v.value.slice(ns.length))) return `${p}:${v.value.slice(ns.length)}`
  return v.value
}

/** A value spelled `text` for a dimension, if there is one (unknown text: undefined). */
function unspell(r: Reader, loc: Location, d: Dimension, text: string): Term | undefined {
  const f = r.f
  const exists = (t: NamedNode) => r.match(t, null, null).length > 0
  if (d.target) {
    const byAnchor = r.match(null, NAV + 'anchor', f.literal(text))[0]?.subject
    if (byAnchor?.termType === 'NamedNode') return byAnchor
    const candidates = text.includes(':') ? [expand(r, text)] : prefixMap(r).map(([, ns]) => ns + text)
    // Only text an IRI can end with (an unknown or malformed segment names nothing).
    const found = /^[\w.~:-]+$/.test(text) ? candidates.filter((c): c is string => !!c).map((c) => f.namedNode(c)).filter(exists) : []
    return found.find((t) => shows(r, loc, t)) ?? found[0]
  }
  const range = one(r, f.namedNode(d.property), RDFS_RANGE)?.value
  if (range?.startsWith(NS.xsd)) {
    if (range === XSD_BOOLEAN) return text === 'true' || text === 'false' ? f.literal(text, f.namedNode(XSD_BOOLEAN)) : undefined
    if (range === XSD_INTEGER) return /^-?\d+$/.test(text) ? f.literal(text, f.namedNode(XSD_INTEGER)) : undefined
    return f.literal(text, f.namedNode(range))
  }
  const typed = (t: Term) => !range || r.match(t, RDF_TYPE, f.namedNode(range)).length > 0
  for (const p of d.prefixed ? [] : new Set([d.keyProperty, SEM.slug])) {
    const hit = r.match(null, p, f.literal(text)).map((q) => q.subject).find(typed)
    if (hit) return hit
  }
  const iri = expand(r, text)
  if (iri && exists(f.namedNode(iri)) && typed(f.namedNode(iri))) return f.namedNode(iri)
  // A literal-valued dimension without a declared range takes text as it comes.
  const current = one(r, d.subject, d.property)
  return current?.termType === 'Literal' ? f.literal(text) : undefined
}

function expand(r: Reader, text: string): string | undefined {
  const m = /^([a-zA-Z][\w-]*):([\w-]+)$/.exec(text)
  if (!m) return undefined
  return prefixMap(r).find(([p]) => p === m[1])?.[1]?.concat(m[2]!)
}

/** Fragment segments keep ':' readable; everything else that needs it is escaped. */
const segment = (s: string) => encodeURIComponent(s).replaceAll('%3A', ':').replaceAll('%40', '@')

/** The dimensions a carrier carries, parents before their children, the target last. */
function ordered(loc: Location, carrier: string): Dimension[] {
  const carried = loc.dimensions.filter((d) => d.carriers.includes(carrier))
  const out: Dimension[] = []
  const visit = (parent: string | undefined) => {
    for (const d of carried) if (!d.target && d.parent === parent && !out.includes(d)) out.push(d), visit(d.iri)
  }
  visit(undefined)
  for (const d of carried) if (!out.includes(d) && !d.target) out.push(d) // a parent not carried here
  return [...out, ...carried.filter((d) => d.target)]
}

/** The location as URL text: the fragment (with '#') and the query parameters it sets. */
export function spellAddress(r: Reader, loc: Location, values: Values): { fragment: string; query: Map<string, string | undefined> } {
  const parts = ordered(loc, NAV + 'Fragment')
    .filter((d) => values.has(d.iri) && inScope(r, loc, d, values))
    .map((d) => segment(spell(r, d, values.get(d.iri)!)))
  const query = new Map<string, string | undefined>()
  for (const d of ordered(loc, NAV + 'Query')) query.set(d.key, values.has(d.iri) && inScope(r, loc, d, values) ? spell(r, d, values.get(d.iri)!) : undefined)
  return { fragment: parts.length ? '#' + parts.join('/') : '', query }
}

/**
 * What a URL says (its query and fragment): values for the dimensions it carries that apply,
 * plus, for a target, the values that show it. Unknown text is ignored: a segment no dimension
 * in scope knows ends the path.
 */
export function readAddress(r: Reader, loc: Location, address: string): Values {
  const url = new URL(address || '', 'https://semblance.invalid/')
  const values: Values = new Map()
  const segments = url.hash
    .slice(1)
    .split('/')
    .filter(Boolean)
    .map((s) => {
      try {
        return decodeURIComponent(s)
      } catch {
        return s
      }
    })
  const fragmentDims = ordered(loc, NAV + 'Fragment')
  for (const text of segments) {
    const open = fragmentDims.filter((d) => !d.target && !values.has(d.iri) && (!d.parent || values.has(d.parent)) && inScope(r, loc, d, values))
    const hit = open.map((d) => [d, unspell(r, loc, d, text)] as const).find(([, v]) => v)
    if (hit) {
      values.set(hit[0].iri, hit[1]!)
      continue
    }
    const target = fragmentDims.find((d) => d.target)
    const v = target && unspell(r, loc, target, text)
    if (target && v) {
      values.set(target.iri, v)
      for (const [dim, val] of shows(r, loc, v) ?? []) values.set(dim, val)
    }
    break
  }
  for (const d of ordered(loc, NAV + 'Query')) {
    const text = url.searchParams.get(d.key)
    const v = text !== null && !values.has(d.iri) ? unspell(r, loc, d, text) : undefined
    if (v) values.set(d.iri, v)
  }
  // Values whose dimension does not apply under what was read are dropped.
  for (const d of loc.dimensions) if (values.has(d.iri) && !d.target && !inScope(r, loc, d, new Map([...currentValues(r, loc), ...values]))) values.delete(d.iri)
  return values
}

// --- where a resource is shown ---------------------------------------------------------------------

/** The way down from the location's root to `resource`: each container, the part it holds, and through what. */
interface Step {
  parent: Term
  child: Term
  via: string
}

/**
 * Walk up the component tree from `resource` (nav:ContainmentProperty; lists through their
 * members) to the location's root, breadth first so the shortest way wins. Undefined when the
 * resource is not under the root.
 */
function pathTo(r: Reader, loc: Location, resource: Term): Step[] | undefined {
  const f = r.f
  const containment = (p: Term) => r.match(p, RDF_TYPE, f.namedNode(NAV + 'ContainmentProperty')).length > 0
  const parents = (x: Term): { parent: Term; via: string }[] => {
    const out: { parent: Term; via: string }[] = []
    const holders = [x]
    for (const cell of r.match(null, RDF_FIRST, x)) {
      let head = cell.subject
      for (let i = 0, prev; i < 10_000 && (prev = r.match(null, RDF_REST, head)[0]); i++) head = prev.subject
      holders.push(head)
    }
    for (const h of holders) for (const q of r.match(null, null, h)) if (q.subject.termType === 'NamedNode' && containment(q.predicate)) out.push({ parent: q.subject, via: q.predicate.value })
    return out
  }
  // Each node remembers how it was reached.
  const from = new Map<string, { node: Term; up?: string; via?: string }>([[resource.value, { node: resource }]])
  const queue: Term[] = [resource]
  let reached: string | undefined = loc.root ? undefined : resource.value
  while (queue.length && !reached && from.size < 20_000) {
    const x = queue.shift()!
    for (const { parent, via } of parents(x)) {
      if (from.has(parent.value)) continue
      from.set(parent.value, { node: parent, up: x.value, via })
      if (parent.value === loc.root) {
        reached = parent.value
        break
      }
      queue.push(parent)
    }
  }
  if (!reached) return undefined
  const steps: Step[] = []
  for (let at = from.get(reached)!; at.up; at = from.get(at.up)!) steps.push({ parent: at.node, child: from.get(at.up)!.node, via: at.via! })
  return steps
}

/**
 * The dimension values that show `resource` under the location: at each c:Switch case on the
 * way down, the value of the dimension the switch follows. Undefined when it is not under the root.
 */
export function shows(r: Reader, loc: Location, resource: Term): Values | undefined {
  const steps = pathTo(r, loc, resource)
  if (!steps) return undefined
  const values: Values = new Map()
  for (const { parent, child } of steps) {
    const when = one(r, child, C + 'when')
    if (!when || r.match(parent, C + 'cases', null).length === 0) continue
    const controls = one(r, parent, C + 'controls')
    const property = one(r, parent, C + 'property')?.value
    const d = loc.dimensions.find((d) => !d.target && same(d.subject, controls) && d.property === property)
    if (d) values.set(d.iri, when)
  }
  return values
}

const isA = (r: Reader, node: Term, cls: string) => {
  const seen = new Set<string>()
  const todo = r.match(node, RDF_TYPE, null).map((q) => q.object.value)
  while (todo.length) {
    const c = todo.pop()!
    if (c === cls) return true
    if (seen.has(c) || seen.size > 256) continue
    seen.add(c)
    for (const q of r.match(r.f.namedNode(c), NS.rdfs + 'subClassOf', null)) todo.push(q.object.value)
  }
  return false
}

/**
 * The rest of what shows `resource`, beyond the location's dimensions: on the way down, each
 * container that hides what it holds until told (nav:reveal on its class: a tab selected, an item
 * expanded, a window opened) is told. Written when a link to it is followed.
 */
export function opening(r: Reader, loc: Location, resource: Term): Delta {
  const f = r.f
  const delta: Delta = { add: [], remove: [] }
  const reveals = r.match(null, NAV + 'reveal', null).flatMap((q) => {
    const via = one(r, q.object, NAV + 'via')?.value
    const sets = one(r, q.object, NAV + 'sets')?.value
    return via && sets ? [{ cls: q.subject.value, via, sets, value: one(r, q.object, NAV + 'value') }] : []
  })
  for (const { parent, child, via } of pathTo(r, loc, resource) ?? []) {
    for (const rv of reveals) {
      if (rv.via !== via || !isA(r, parent, rv.cls)) continue
      const value = rv.value ?? child
      const now = r.match(parent, rv.sets, null)
      if (now.length === 1 && same(now[0]!.object, value)) continue
      delta.remove.push(...now)
      delta.add.push(f.quad(parent as Quad['subject'], f.namedNode(rv.sets), value as Quad['object']))
    }
  }
  return delta
}

/** The fragment of the location that shows `resource`, with it as the target. */
export function addressOf(r: Reader, resource: Term): string | undefined {
  const locations = readLocations(r)
  for (const loc of locations) {
    const needs = shows(r, loc, resource)
    if (!needs) continue
    // A resource that is a whole case of a switch (a page) is shown by the values alone: there is
    // nothing within it to bring into view, so the address names no target (#home, not #home/home).
    const last = pathTo(r, loc, resource)?.at(-1)
    const whole = !!last && !!one(r, last.parent, C + 'when')
    const target = loc.dimensions.find((d) => d.target && d.carriers.includes(NAV + 'Fragment'))
    if (target && !whole) needs.set(target.iri, resource)
    return spellAddress(r, loc, needs).fragment || '#'
  }
  // A term no component shows (a class, a property): the dimension whose values are terms (nav:showsTerms),
  // with the values its parents need (nav:when), e.g. #reference/c:List.
  if (resource.termType !== 'NamedNode' || !isTerm(r, resource)) return undefined
  for (const loc of locations) {
    const d = loc.dimensions.find((x) => x.terms && x.carriers.includes(NAV + 'Fragment'))
    if (!d) continue
    const values: Values = new Map([[d.iri, resource]])
    for (let at: Dimension | undefined = d, n = 0; at?.parent && n < 16; n++) {
      const parent = loc.dimensions.find((x) => x.iri === at!.parent)
      if (parent && at.when[0]) values.set(parent.iri, at.when[0])
      at = parent
    }
    return spellAddress(r, loc, values).fragment || '#'
  }
  return undefined
}

/**
 * Whether a resource is a term of a vocabulary: used as a class or as a property, or named in the
 * namespace of an owl:Ontology the graph has (a framework class, a reaction it declares).
 */
function isTerm(r: Reader, t: Term): boolean {
  if (r.match(null, RDF_TYPE, t).length || r.match(null, t.value, null).length) return true
  const ns = t.value.slice(0, Math.max(t.value.lastIndexOf('#'), t.value.lastIndexOf('/')) + 1)
  return [ns, ns.slice(0, -1)].some((o) => r.match(r.f.namedNode(o), RDF_TYPE, r.f.namedNode(NS.owl + 'Ontology')).length > 0)
}

/** The change that makes the graph say what `address` says (a target absent from it is cleared). */
export function followDelta(r: Reader, loc: Location, address: string): Delta {
  const f = r.f
  const read = readAddress(r, loc, address)
  const delta: Delta = { add: [], remove: [] }
  for (const d of loc.dimensions) {
    if (!d.carriers.some((c) => URL_CARRIERS.has(c))) continue
    const next = read.get(d.iri)
    const now = r.match(d.subject, d.property, null)
    if (next ? now.length === 1 && same(now[0]!.object, next) : !d.target) continue
    delta.remove.push(...now)
    if (next) delta.add.push(f.quad(d.subject, f.namedNode(d.property), next as Quad['object']))
  }
  // A target in a tab not selected, an item not expanded, a window not open: they are opened too.
  const target = loc.dimensions.find((d) => d.target && read.has(d.iri))
  if (target) {
    const open = opening(r, loc, read.get(target.iri)!)
    delta.remove.push(...open.remove)
    delta.add.push(...open.add)
  }
  return delta
}

const storeReader = (store: GraphStore): Reader => ({ f: store.factory, match: (s, p, o) => store.match(s, p ? store.factory.namedNode(p) : null, o, store.factory.defaultGraph()) })
const ruleReader = (ctx: RuleContext): Reader => ({ f: ctx.f, match: (s, p, o) => ctx.match(s, p, o) })

// --- the host effect -----------------------------------------------------------------------------

/** The parts of window.location the effect reads. */
export interface UrlLike {
  readonly pathname: string
  readonly search: string
  readonly hash: string
}
export interface HistoryLike {
  pushState(data: unknown, unused: string, url: string): void
  replaceState(data: unknown, unused: string, url: string): void
}

export interface NavigationOptions {
  /** Serve the location that belongs to this root (nav:root); default: the first declared. */
  root?: string
  location?: UrlLike
  history?: HistoryLike
  /** Where popstate and hashchange are heard (default: the window). */
  events?: EventTarget
  /** The persistence effect, which restores dimensions carried by storage scopes (restore it first). */
  persistence?: { restored(policy: string): boolean }
  /** Bring a target's rendered root into view; false while it is not rendered yet (see rendererReveal). */
  reveal?: (iri: string, opts: { smooth: boolean }) => boolean
  /** When to retry a reveal (default: the next animation frame). */
  frame?: (run: () => void) => void
  /** When to write the URL after a change (default: a microtask, so one interaction is one step). */
  schedule?: (run: () => void) => void
}

/** The navigation effect for one location of one store. */
export class Navigation {
  private runtime?: Runtime
  private bus?: EventBus
  private onNavigate?: () => void
  private pending = false
  private written?: Values
  private revealing?: string
  private revealer?: NavigationOptions['reveal']
  private model?: Location | null
  private readonly r: Reader

  constructor(
    readonly store: GraphStore,
    readonly opts: NavigationOptions = {},
  ) {
    this.r = storeReader(store)
  }

  /** The location this effect serves (re-read when navigation declarations change). */
  location(): Location | undefined {
    if (this.model === undefined) {
      const all = readLocations(this.r)
      this.model = (this.opts.root ? all.find((l) => l.root === this.opts.root) : all[0]) ?? null
    }
    return this.model ?? undefined
  }

  private get url(): UrlLike {
    return this.opts.location ?? globalThis.location
  }
  private get history(): HistoryLike {
    return this.opts.history ?? globalThis.history
  }
  private address() {
    return this.url.search + this.url.hash
  }

  /**
   * Read the URL into the graph. Call before projecting (after the persistence effect's
   * restore), so the first render is already where the URL says. Per dimension, the carrier
   * with the best nav:rank that has a value wins; storage carriers have been read by the
   * persistence effect, so a storage value that outranks the URL is kept.
   */
  async restore(): Promise<number> {
    const loc = this.location()
    if (!loc) return 0
    const read = readAddress(this.r, loc, this.address())
    const f = this.store.factory
    let n = 0
    for (const d of loc.dimensions) {
      const next = read.get(d.iri)
      // The first carrier that has a value wins; none: the graph's own value (the default).
      const winner = d.carriers.find((c) => (URL_CARRIERS.has(c) ? !!next : !!this.opts.persistence?.restored(d.iri)))
      if (!winner || !URL_CARRIERS.has(winner)) continue // storage's value is in the graph already
      const now = this.store.match(d.subject, f.namedNode(d.property), null, f.defaultGraph())
      if (now.length === 1 && same(now[0]!.object, next)) continue
      for (const q of now) this.store.delete(q)
      this.store.add(f.quad(d.subject, f.namedNode(d.property), next as Quad['object'], f.defaultGraph()))
      n++
    }
    const target = loc.dimensions.find((d) => d.target)
    if (target && read.has(target.iri)) {
      this.revealing = read.get(target.iri)!.value
      const open = opening(this.r, loc, read.get(target.iri)!)
      for (const q of open.remove) this.store.delete(q)
      for (const q of open.add) this.store.add(f.quad(q.subject, q.predicate, q.object, f.defaultGraph()))
    }
    return n
  }

  /** Follow input changes and the URL from now on. With a bus, URL changes are recorded as nav:Navigate events. */
  attach(runtime: Runtime, opts: { bus?: EventBus; onNavigate?: () => void; reveal?: NavigationOptions['reveal'] } = {}): () => void {
    this.runtime = runtime
    this.bus = opts.bus
    this.onNavigate = opts.onNavigate
    this.revealer = opts.reveal ?? this.opts.reveal
    this.write(false)
    if (this.revealing) this.reveal(this.revealing)
    const stop = runtime.onInput((graph, delta) => this.changed(graph, delta))
    const events = this.opts.events ?? (globalThis as unknown as EventTarget)
    const follow = () => this.follow()
    events.addEventListener?.('popstate', follow)
    events.addEventListener?.('hashchange', follow)
    return () => {
      stop()
      events.removeEventListener?.('popstate', follow)
      events.removeEventListener?.('hashchange', follow)
    }
  }

  private changed(graph: string | undefined, delta: Delta) {
    if (graph !== undefined) return
    // Requests to go somewhere (nav:goTo), performed once this change is settled.
    const goes = delta.add.filter((q) => q.predicate.value === GO_TO)
    if (goes.length) {
      const first = !this.going.length
      this.going.push(...goes)
      if (first) (this.opts.schedule ?? queueMicrotask)(() => this.go())
    }
    const loc = this.location()
    let touched = false
    for (const q of [...delta.add, ...delta.remove]) {
      if (q.predicate.value === GO_TO) continue
      if (q.predicate.value.startsWith(NAV)) this.model = undefined
      if (this.model === undefined || loc?.dimensions.some((d) => d.property === q.predicate.value && d.subject.equals(q.subject))) touched = true
    }
    if (!touched || this.pending) return
    this.pending = true
    ;(this.opts.schedule ?? queueMicrotask)(() => this.pending && this.flush())
  }

  /** Requests to go somewhere (nav:goTo) not performed yet. */
  private going: Quad[] = []

  /**
   * Perform the nav:goTo requests: they are taken out of the graph, and the last one is gone to
   * as a followed link to its resource is: a step in the history, then followed (a nav:Navigate
   * event, the resource brought into view). A resource no location shows goes nowhere.
   */
  private go() {
    const requests = this.going.splice(0)
    const runtime = this.runtime
    if (!runtime || !requests.length) return
    const f = this.store.factory
    const stated = requests.filter((q) => this.store.match(q.subject, q.predicate, q.object, f.defaultGraph()).length)
    if (stated.length) runtime.apply({ add: [], remove: stated })
    const address = addressOf(this.r, requests.at(-1)!.object)
    if (address === undefined) return
    // Anything the change wrote is spelled first, so the step goes from where it left us.
    if (this.pending) this.flush()
    const href = this.url.pathname + this.url.search + (address.startsWith('#') ? address : '#' + address)
    if (href === this.url.pathname + this.url.search + this.url.hash) this.history.replaceState(null, '', href)
    else this.history.pushState(null, '', href)
    this.follow()
  }

  /** Write the graph's location to the URL now (if it changed). */
  flush(): void {
    this.pending = false
    this.write(true)
  }

  /** The URL as the graph would spell it now. */
  href(): string | undefined {
    const loc = this.location()
    if (!loc) return undefined
    const { fragment, query } = spellAddress(this.r, loc, currentValues(this.r, loc))
    const params = new URLSearchParams(this.url.search)
    for (const [k, v] of query) v === undefined ? params.delete(k) : params.set(k, v)
    const search = params.toString()
    return this.url.pathname + (search ? '?' + search : '') + fragment
  }

  private write(asStep: boolean) {
    const loc = this.location()
    const runtime = this.runtime
    if (!loc || !runtime) return
    const values = currentValues(this.r, loc)
    // A target applies while the location shows it; moving elsewhere leaves it.
    const stale = loc.dimensions.filter((d) => d.target && values.has(d.iri) && !inScope(this.r, loc, d, values))
    if (stale.length) {
      const f = this.store.factory
      this.pending = true // the removal is part of this step
      runtime.apply({ add: [], remove: stale.flatMap((d) => this.store.match(d.subject, f.namedNode(d.property), null, f.defaultGraph())) })
      for (const d of stale) values.delete(d.iri)
      this.pending = false
    }
    const next = this.href()!
    const previous = this.written
    this.written = values
    if (next === this.url.pathname + this.url.search + this.url.hash) return this.report(loc)
    const urlDims = loc.dimensions.filter((d) => d.carriers.some((c) => URL_CARRIERS.has(c)))
    const step = asStep && !!previous && urlDims.some((d) => d.makesHistory && !same(previous.get(d.iri), values.get(d.iri)))
    if (step) this.history.pushState(null, '', next)
    else this.history.replaceState(null, '', next)
    this.report(loc)
  }

  /**
   * For inspection, in the navigation graph: the address the URL carriers spell, and per
   * dimension its value, whether it applies and how the address spells it. The location's
   * nav:revision counts the reports, so a view of them can follow the location alone.
   */
  private report(loc: Location) {
    const runtime = this.runtime
    if (!runtime) return
    const f = this.store.factory
    const g = f.namedNode(NAV_GRAPH)
    const nn = (x: string) => f.namedNode(x)
    const s = nn(loc.iri)
    const values = currentValues(this.r, loc)
    const add: Quad[] = [f.quad(s, nn(NAV + 'address'), f.literal(this.address() || '#'))]
    for (const d of loc.dimensions) {
      const v = values.get(d.iri)
      const applies = !!v && inScope(this.r, loc, d, values)
      add.push(f.quad(nn(d.iri), nn(NAV + 'applies'), f.literal(String(applies), nn(XSD_BOOLEAN))))
      if (v) add.push(f.quad(nn(d.iri), nn(NAV + 'value'), v as Quad['object']))
      if (applies) add.push(f.quad(nn(d.iri), nn(NAV + 'spelled'), f.literal(spell(this.r, d, v!))))
    }
    const reported = (q: Quad) => q.predicate.value !== NAV + 'revision'
    const old = [s, ...loc.dimensions.map((d) => nn(d.iri))].flatMap((x) => this.store.match(x, null, null, g)).filter(reported)
    const key = (q: Quad) => q.subject.value + ' ' + q.predicate.value + ' ' + q.object.termType + q.object.value
    const before = new Set(old.map(key))
    if (old.length === add.length && add.every((q) => before.has(key(q)))) return
    const revision = this.store.match(s, nn(NAV + 'revision'), null, g)
    const n = Number(revision[0]?.object.value ?? 0) + 1
    runtime.write(NAV_GRAPH, { remove: [...old, ...revision], add: [...add, f.quad(s, nn(NAV + 'revision'), f.literal(String(n), nn(XSD_INTEGER)))] })
  }

  /**
   * The URL changed outside the graph (Back, Forward, a typed or followed link): write what it
   * says into the graph (a nav:Navigate event when there is a bus), then spell it back in place.
   */
  follow(address = this.address()): void {
    const loc = this.location()
    const runtime = this.runtime
    if (!loc || !runtime) return
    if (this.bus) this.bus.emit(NAV + 'Navigate', loc.iri, { value: address })
    else runtime.apply(followDelta(this.r, loc, address))
    this.pending = false
    this.write(false)
    this.onNavigate?.()
    const target = loc.dimensions.find((d) => d.target)
    const now = target && currentValues(this.r, loc).get(target.iri)
    if (now) this.reveal(now.value)
  }

  /** Bring a target into view once it is rendered (it may be parked until its page shows). */
  private reveal(iri: string, tries = 120) {
    this.revealing = iri
    const reveal = this.revealer
    if (!reveal) return
    const attempt = (left: number) => {
      if (this.revealing !== iri) return
      if (reveal(iri, { smooth: this.smooth() })) return void (this.revealing = undefined)
      if (left > 0) (this.opts.frame ?? ((run) => (globalThis.requestAnimationFrame ?? ((cb: () => void) => setTimeout(cb, 16)))(run)))(() => attempt(left - 1))
    }
    attempt(tries)
  }

  /** Smooth unless the location's root asks for less motion (mot:preference), or follows the system and it does. */
  private smooth(): boolean {
    const root = this.location()?.root
    const f = this.store.factory
    const pref = root && this.store.match(f.namedNode(root), f.namedNode(NS.mot + 'preference'), null)[0]?.object
    return !motionCalmed(pref ? this.store.match(pref as NamedNode, f.namedNode(SEM.slug), null)[0]?.object.value : undefined)
  }
}

/** Reveal in a renderer's DOM: scroll the target's rendered root into view, and focus it if it can be. */
export function rendererReveal(renderer: { nodeOf(key: string): Node | undefined }): NonNullable<NavigationOptions['reveal']> {
  return (iri, { smooth }) => {
    const el = renderer.nodeOf(iri + '/~root') as HTMLElement | undefined
    if (!el?.isConnected || el.closest('[hidden]')) return false
    // Centered when it fits (a sticky app bar would cover its start), else from its start.
    const fits = el.getBoundingClientRect?.().height < (el.ownerDocument.defaultView?.innerHeight ?? 0) * 0.8
    el.scrollIntoView?.({ behavior: smooth ? 'smooth' : 'auto', block: fits ? 'center' : 'start' })
    if (el.matches('a[href], button, input, select, textarea, [tabindex]')) el.focus({ preventScroll: true })
    return true
  }
}

// --- rules and reactions -------------------------------------------------------------------------

/** c:LinkDestination: a link to a resource (c:to) gets the address of the location that shows it. */
const linkDestination: FnRule = (ctx, focus) => {
  const to = ctx.object(focus, C + 'to')
  if (to?.termType !== 'NamedNode') return []
  const href = addressOf(ruleReader(ctx), to)
  return href === undefined ? [] : ctx.out(focus).t(focus, NAV + 'href', href).quads
}

/**
 * c:LinkCurrent: the links that go to the address the location shows now (its fragment, as links
 * spell it) are nav:current. Found by their address, so a step costs the links it changes.
 */
const linkCurrent: FnRule = (ctx, focus) => {
  const address = ctx.object(focus, NAV + 'address')?.value
  if (address === undefined) return []
  const fragment = address.includes('#') ? address.slice(address.indexOf('#')) : '#'
  const r = ruleReader(ctx)
  const links = [
    ...ctx.match(null, NAV + 'href', ctx.f.literal(fragment)).map((q) => q.subject),
    ...ctx.match(null, C + 'href', ctx.f.literal(fragment)).map((q) => q.subject).filter((s) => !ctx.object(s, NAV + 'href')),
  ].filter((s) => isA(r, s, C + 'Link'))
  const o = ctx.out(focus)
  const yes = ctx.f.literal('true', ctx.nn(XSD_BOOLEAN))
  for (const link of new Set(links)) o.t(link, NAV + 'current', yes)
  return o.quads
}

/** nav:Follow: what the URL in the event says, written into the graph. */
const follow: ReactionFn = (ctx) => {
  const r = storeReader(ctx.runtime.store)
  const loc = readLocations(r).find((l) => l.iri === ctx.target.value)
  const address = ctx.object(ctx.event, NS.beh + 'value')?.value
  return loc && address !== undefined ? followDelta(r, loc, address) : { add: [], remove: [] }
}

/**
 * nav:Containment: each link of this containment property, from a container to what it holds
 * (directly or through an RDF list), as an edge of its own (for the destinations' SPARQL).
 */
const containment: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const edge = (parent: Term, child: Term) => {
    const e = o.node('up-' + md5(parent.value + ' ' + child.value))
    o.t(e, NAV + 'from', child).t(e, NAV + 'to', parent)
  }
  for (const q of ctx.match(null, focus, null)) {
    if (q.subject.termType !== 'NamedNode') continue
    if (q.object.termType === 'NamedNode') edge(q.subject, q.object)
    else for (const member of list(ruleReader(ctx), q.object)) if (member.termType === 'NamedNode') edge(q.subject, member)
  }
  return o.quads
}

export const navigationFunctions: Record<string, FnRule> = { 'navigation:link-destination': linkDestination, 'navigation:link-current': linkCurrent, 'navigation:containment': containment }
export const locationReactions: Record<string, ReactionFn> = { 'navigation:follow': follow }
