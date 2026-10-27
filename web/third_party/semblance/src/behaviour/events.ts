import type { NamedNode, Quad, Quad_Graph, Term } from '@rdfjs/types'
import { BEH, EVENTS_GRAPH, KEY, LDES, NS, RDF_TYPE, RDFS_SUBCLASS, SEM, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { type Delta, MINT_SEPARATOR, owningFocus, parseDuration, quadKey } from '../rdf.ts'
import { prefixesFor } from '../prefixes.ts'
import type { DomRenderer } from '../render/dom.ts'
import type { Projection, Runtime } from '../runtime.ts'
import { comboOf, mirrored, modifiersOf, navigate, normalizeShortcut, rightToLeft, rove, trapTab } from '../layers/keyboard.ts'
import { reactions as builtinReactions } from './reactions.ts'
import { DragHost } from './drag.ts'
import { returnPlaces, tearGesture } from './tear.ts'
import { type Clock, systemClock } from './clock.ts'

const XSD_DATETIME = 'http://www.w3.org/2001/XMLSchema#dateTime'
/** Distinguishes event IRIs across page loads sharing a store snapshot. */
const RUN = Date.now().toString(36)

// Re-exported: persistence imports it from rdf.ts, so it does not import this module (which imports it back).
export { parseDuration }

export interface ReactionContext {
  runtime: Runtime
  event: NamedNode
  /** The component instance the event is addressed to. */
  target: NamedNode
  /** Match over input, events and the projection's derived graph. */
  match(s: Term | null, p: string | null, o: Term | null): Quad[]
  object(s: Term, p: string): Term | undefined
}

export type ReactionFn = (ctx: ReactionContext) => Delta

export interface EventDetail {
  /** The RDF/HTML node the interaction hit. */
  node?: string
  value?: string
  checked?: boolean
  /** Numeric details (beh:x, beh:y, beh:width, beh:height). */
  numbers?: Record<string, number>
  /** A text control's caret (selectionStart) after the interaction (beh:caret). */
  caret?: number
  /** The key combination of a key press, normalized as key:shortcut is ("tab", "mod+enter"; beh:key). */
  key?: string
  /** The modifier keys held during a pointer interaction, or the press that moved the focus ("shift", "mod+shift"; beh:modifiers). */
  modifiers?: string
  /** Further statements about the event, by predicate (a drop carries the drag's: what, from, where). */
  about?: [predicate: string, object: Term][]
}

export interface Handled {
  /** False when no reaction produced anything (the event was not recorded). */
  recorded: boolean
  event: NamedNode
  type: string
  target: NamedNode
  reactions: string[]
  delta: Delta
}

interface Reaction {
  iri: string
  when: string
  source?: string
  sourceClass?: string
  deletes?: string
  inserts?: string
  fn?: string
  /** beh:undoable: its effect is recorded on the event's member, for undo. */
  undoable?: boolean
}

/**
 * Records relevant interactions as RDF events and applies the reactions that
 * match them. An event is addressed to the innermost component (walking up
 * from the DOM target) that has a reaction for its class; reactions are
 * evaluated against the state before the event and applied as one delta.
 *
 * Each event, with the size of the change it caused, becomes an immutable
 * member of the beh:ChangeLog LDES; members no retention policy of the
 * stream's view keeps any more (count or age) are retired.
 */
export class EventBus {
  readonly graph: NamedNode
  private sequence = 0
  private readonly graphs: Quad_Graph[]
  /** Retained members, oldest first, so retirement touches only what expires. */
  private retained?: { event: NamedNode; at: number }[]
  /** What time an event happens at (beh:at): the host's clock, which tests can move by hand. */
  readonly clock: Clock

  constructor(
    readonly runtime: Runtime,
    readonly projection: Projection,
    private readonly opts: { functions?: Record<string, ReactionFn>; prefer?: 'sparql' | 'function'; stream?: string; clock?: Clock } = {},
  ) {
    this.clock = opts.clock ?? systemClock()
    const f = runtime.store.factory
    this.graph = f.namedNode(EVENTS_GRAPH)
    this.graphs = [f.defaultGraph(), this.graph, projection.engine.graph]
    // Reactions and the class hierarchy are read once and kept until the input changes
    // them (a reaction's triples, a type, a subclass link).
    runtime.onInput((graph, delta) => {
      if (graph !== undefined) return
      for (const q of [...delta.add, ...delta.remove]) {
        const p = q.predicate.value
        if (p.startsWith(NS.beh) || p === SEM.function || p === RDFS_SUBCLASS) this.cachedReactions = undefined
        if (p === RDF_TYPE || p === RDFS_SUBCLASS) this.classCache.clear()
      }
    })
    // Types the projection derives (minted components) change a node's classes too.
    projection.engine.onChange((delta) => {
      for (const q of [...delta.add, ...delta.remove]) if (q.predicate.value === RDF_TYPE) this.classCache.delete(q.subject.value)
    })
  }

  private cachedReactions?: Map<string, Reaction[]>
  private classCache = new Map<string, Set<string>>()

  private get f() {
    return this.runtime.store.factory
  }

  private match = (s: Term | null, p: string | null, o: Term | null): Quad[] =>
    this.graphs.flatMap((g) => this.runtime.store.match(s, p ? this.f.namedNode(p) : null, o, g))

  private value(s: Term, p: string): Term | undefined {
    return this.match(s, p, null)[0]?.object
  }

  /** Reactions by the event class they handle (cached; see the constructor). */
  private reactionsByEvent(): Map<string, Reaction[]> {
    if (this.cachedReactions) return this.cachedReactions
    const byEvent = new Map<string, Reaction[]>()
    for (const r of this.reactions()) {
      let list = byEvent.get(r.when)
      if (!list) byEvent.set(r.when, (list = []))
      list.push(r)
    }
    for (const list of byEvent.values()) list.sort((a, b) => a.iri.localeCompare(b.iri))
    return (this.cachedReactions = byEvent)
  }

  private reactions(): Reaction[] {
    const nn = (iri: string) => this.f.namedNode(iri)
    return this.runtime.store.match(null, nn(RDF_TYPE), nn(BEH.Reaction), this.f.defaultGraph()).map(({ subject: r }) => ({
      iri: r.value,
      when: this.value(r, BEH.when)?.value ?? '',
      source: this.value(r, BEH.source)?.value,
      sourceClass: this.value(r, BEH.sourceClass)?.value,
      deletes: this.value(r, BEH.deletes)?.value,
      inserts: this.value(r, BEH.inserts)?.value,
      fn: this.value(r, SEM.function)?.value,
      undoable: this.value(r, BEH.undoable)?.value === 'true',
    }))
  }

  /** Classes of a node, with superclasses. */
  private classes(node: string): Set<string> {
    const cached = this.classCache.get(node)
    if (cached) return cached
    const out = new Set<string>()
    const todo = this.match(this.f.namedNode(node), RDF_TYPE, null).map((q) => q.object.value)
    while (todo.length) {
      const c = todo.pop()!
      if (out.has(c)) continue
      out.add(c)
      for (const q of this.runtime.store.match(this.f.namedNode(c), this.f.namedNode(RDFS_SUBCLASS), null)) todo.push(q.object.value)
    }
    this.classCache.set(node, out)
    return out
  }

  /** Reactions for an event class on a component instance. */
  reactionsFor(type: string, target: string): Reaction[] {
    const candidates = this.reactionsByEvent().get(type)
    if (!candidates) return []
    const types = this.classes(target)
    return candidates.filter((r) => r.source === target || (r.sourceClass !== undefined && types.has(r.sourceClass)))
  }

  /** Where the pointer was last seen (view state, for commands that act "here"). */
  private pointer?: { x: number; y: number }

  /** The pointer's place for a command event: the RDF/HTML node under it and its distance to the viewport's edges. */
  private pointerDetail(renderer: DomRenderer): EventDetail {
    const p = this.pointer
    const view = renderer.host.ownerDocument.defaultView
    if (!p || !view) return {}
    let node: string | undefined
    for (let el: Node | null = renderer.host.ownerDocument.elementFromPoint?.(p.x, p.y) ?? null; el && el !== renderer.host; el = el.parentNode) {
      node = renderer.keyOf(el)
      if (node) break
    }
    return { node, numbers: { x: p.x, y: p.y, toEnd: view.innerWidth - p.x, toBottom: view.innerHeight - p.y } }
  }

  /** The key:Command bound to a (normalized) key combination, if any. */
  commandFor(combo: string): string | undefined {
    const store = this.runtime.store
    const type = this.f.namedNode(RDF_TYPE)
    const command = this.f.namedNode(KEY.Command)
    for (const q of store.match(null, this.f.namedNode(KEY.shortcut), null, this.f.defaultGraph())) {
      if (normalizeShortcut(q.object.value) === combo && store.match(q.subject, type, command, this.f.defaultGraph()).length) return q.subject.value
    }
    return undefined
  }

  /** Event classes recorded from a DOM event type. */
  eventClasses(domEvent: string): string[] {
    return this.runtime.store
      .match(null, this.f.namedNode(BEH.domEvent), this.f.literal(domEvent), this.f.defaultGraph())
      .map((q) => q.subject.value)
  }

  get stream(): NamedNode {
    return this.f.namedNode(this.opts.stream ?? BEH.ChangeLog)
  }

  /** Record an event on `target`, apply its reactions, and append it to the change log. */
  emit(type: string, target: string, detail: EventDetail = {}, reactions = this.reactionsFor(type, target)): Handled {
    // An event, its reactions and the tick they cause are one turn of the watchdog's budget.
    return this.runtime.watchdog.turn(() => this.emitWatched(type, target, detail, reactions))
  }

  private emitWatched(type: string, target: string, detail: EventDetail, reactions: Reaction[]): Handled {
    const { f } = this
    const nn = (iri: string) => f.namedNode(iri)
    // A restored log (persistence) continues where it left off.
    if (this.sequence === 0) {
      for (const q of this.runtime.store.match(null, f.namedNode(BEH.sequence), null, this.graph)) this.sequence = Math.max(this.sequence, Number(q.object.value) || 0)
    }
    const seq = ++this.sequence
    const event = nn(`urn:semblance:event:${RUN}-${seq}`)
    const wd = this.runtime.watchdog
    wd.startedBy(event.value, type)
    const quads = [
      f.quad(event, nn(RDF_TYPE), nn(type)),
      f.quad(event, nn(BEH.target), nn(target)),
      f.quad(event, nn(BEH.sequence), f.literal(String(seq), nn(XSD_INTEGER))),
      f.quad(event, nn(BEH.at), f.literal(new Date(this.clock.now()).toISOString(), nn(XSD_DATETIME))),
    ]
    if (detail.node !== undefined) quads.push(f.quad(event, nn(BEH.node), nn(detail.node)))
    if (detail.value !== undefined) quads.push(f.quad(event, nn(BEH.value), f.literal(detail.value)))
    if (detail.checked !== undefined) quads.push(f.quad(event, nn(BEH.checked), f.literal(String(detail.checked), nn(XSD_BOOLEAN))))
    if (detail.caret !== undefined) quads.push(f.quad(event, nn(NS.beh + 'caret'), f.literal(String(detail.caret), nn(XSD_INTEGER))))
    if (detail.key !== undefined) quads.push(f.quad(event, nn(NS.beh + 'key'), f.literal(detail.key)))
    if (detail.modifiers) quads.push(f.quad(event, nn(NS.beh + 'modifiers'), f.literal(detail.modifiers)))
    for (const [p, o] of detail.about ?? []) quads.push(f.quad(event, nn(p), o as NamedNode))
    for (const [name, v] of Object.entries(detail.numbers ?? {})) quads.push(f.quad(event, nn(NS.beh + name), f.literal(String(Math.round(v)), nn(XSD_INTEGER))))
    // The event is readable by reactions before it is a member of the stream.
    for (const q of quads) this.runtime.store.add(f.quad(q.subject, q.predicate, q.object, this.graph))

    // Every reaction sees the state before the event. Only those that change
    // something handled it (generic reactions often match and do nothing).
    const delta: Delta = { add: [], remove: [] }
    // What undoable reactions changed (recorded with the member, for undo).
    const undoable = { add: new Set<string>(), remove: new Set<string>() }
    const acted: Reaction[] = []
    for (const r of reactions) {
      // Reactions are atomic (they see the state before the event); each is timed, so a slow one is blamed.
      const t0 = wd.clock()
      const d = this.react(r, event, nn(target))
      wd.note('reaction', r.iri, target, 1, wd.clock() - t0)
      if (!d.add.length && !d.remove.length) continue
      acted.push(r)
      delta.remove.push(...d.remove)
      delta.add.push(...d.add)
      if (r.undoable) for (const [k, qs] of [['add', d.add], ['remove', d.remove]] as const) for (const q of qs) undoable[k].add(quadKey(q))
    }
    reactions = acted
    for (const r of acted) quads.push(f.quad(event, nn(BEH.handledBy), nn(r.iri)))
    const forget = () => {
      for (const q of quads) this.runtime.store.delete(f.quad(q.subject, q.predicate, q.object, this.graph))
    }
    // Events the reactions raise (`?node beh:raise ?class`) are requests, not state: taken out of
    // the change and raised once this event is applied and recorded.
    const raised = delta.add.filter((q) => q.predicate.value === BEH.raise && q.subject.termType === 'NamedNode' && q.object.termType === 'NamedNode')
    if (raised.length) delta.add = delta.add.filter((q) => q.predicate.value !== BEH.raise)
    if (!delta.add.length && !delta.remove.length && !raised.length) {
      forget()
      return { recorded: false, event, type, target: nn(target), reactions: [], delta }
    }
    const handled = this.record(type, target, event, quads, delta, reactions, forget, undoable)
    this.raise(raised, event)
    return handled
  }

  /** How deep raised events may raise further ones (a reaction raising itself stops here). */
  private raising = 0

  /** Raise each requested event once, in a stable order: a click on a component is addressed to its root. */
  private raise(requests: Quad[], by: NamedNode) {
    if (!requests.length || this.raising >= 8) return
    const seen = new Set<string>()
    const todo = requests
      .filter((q) => !seen.has(q.subject.value + ' ' + q.object.value) && seen.add(q.subject.value + ' ' + q.object.value))
      .sort((a, b) => (a.subject.value + a.object.value < b.subject.value + b.object.value ? -1 : 1))
    this.raising++
    try {
      for (const q of todo) {
        const detail: EventDetail = { about: [[BEH.raisedBy, by]] }
        if (q.object.value === BEH.Click) detail.node = q.subject.value + MINT_SEPARATOR + 'root'
        this.emit(q.object.value, q.subject.value, detail)
      }
    } finally {
      this.raising--
    }
  }

  /** Apply an event's change and append it to the change log, as one tick. */
  private record(type: string, target: string, event: NamedNode, quads: Quad[], delta: Delta, reactions: Reaction[], forget: () => void, undoable: { add: Set<string>; remove: Set<string> }): Handled {
    const { f } = this
    const nn = (iri: string) => f.namedNode(iri)
    // The state change and the member that records it are one tick: views over the log and
    // over the state agree after every event (docs/IVM.md, decision 8).
    return this.runtime.transaction(() => {
      const applied = this.runtime.apply(delta)
      // Event classes marked beh:recorded false (e.g. hovers) change state but stay out of the log.
      if (this.value(nn(type), BEH.recorded)?.value === 'false') {
        forget()
        return { recorded: false, event, type, target: nn(target), reactions: reactions.map((r) => r.iri), delta: applied }
      }

      // Completing the member: its effect, its predecessor, then the tree:member link
      // and the stream's head pointer.
      const int = (n: number) => f.literal(String(n), nn(XSD_INTEGER))
      const remove = this.expired()
      const head = this.runtime.store.match(this.stream, nn(BEH.latest), null, this.graph)
      this.runtime.log({
        remove: [...remove, ...head],
        add: [
          ...quads,
          ...this.statements(event, applied, undoable),
          f.quad(event, nn(BEH.added), int(applied.add.length)),
          f.quad(event, nn(BEH.removed), int(applied.remove.length)),
          ...head.map((q) => f.quad(event, nn(BEH.previous), q.object)),
          f.quad(this.stream, nn(LDES.member), event),
          f.quad(this.stream, nn(BEH.latest), event),
        ],
      })
      this.members().push({ event, at: Date.now() })
      return { recorded: true, event, type, target: nn(target), reactions: reactions.map((r) => r.iri), delta: applied }
    })
  }

  /**
   * The effective change of an event's undoable reactions, as rdf:Statement nodes minted under its
   * member: beh:asserted (added), beh:retracted (removed). Only what actually changed is recorded.
   */
  private statements(event: NamedNode, applied: Delta, undoable: { add: Set<string>; remove: Set<string> }): Quad[] {
    if (!undoable.add.size && !undoable.remove.size) return []
    const { f } = this
    const nn = (iri: string) => f.namedNode(iri)
    const out: Quad[] = []
    let i = 0
    for (const [p, qs, keep] of [[BEH.asserted, applied.add, undoable.add], [BEH.retracted, applied.remove, undoable.remove]] as const) {
      for (const q of qs) {
        if (!keep.has(quadKey(q))) continue
        const st = nn(`${event.value}/~change-${i++}`)
        out.push(
          f.quad(event, nn(p), st),
          f.quad(st, nn(RDF_TYPE), nn(NS.rdf + 'Statement')),
          f.quad(st, nn(NS.rdf + 'subject'), q.subject as NamedNode),
          f.quad(st, nn(NS.rdf + 'predicate'), q.predicate as NamedNode),
          f.quad(st, nn(NS.rdf + 'object'), q.object),
        )
      }
    }
    return out
  }

  /**
   * Members (with their triples) that no retention policy of the stream's view keeps
   * once one more member is appended. Policies: beh:LatestMembersPolicy (ldes:amount)
   * and ldes:DurationAgoPolicy (tree:value, or the older ldes:fullLogDuration). No
   * policy keeps everything. Only the retired members are touched.
   */
  private expired(): Quad[] {
    const store = this.runtime.store
    const view = this.value(this.stream, LDES.view)
    const policies = view ? this.match(view, LDES.retentionPolicy, null).map((q) => q.object) : []
    if (!policies.length) return []
    const keeps: ((index: number, at: number, size: number) => boolean)[] = policies.map((p) => {
      const amount = this.value(p, LDES.amount)
      if (amount) return (i, _at, size) => size - i <= Number(amount.value)
      const duration = this.value(p, NS.tree + 'value') ?? this.value(p, LDES.fullLogDuration)
      const ms = duration ? parseDuration(duration.value) : undefined
      if (ms !== undefined) {
        const cutoff = Date.now() - ms
        return (_i, at) => at >= cutoff
      }
      return () => true
    })
    const members = this.members()
    const size = members.length + 1
    const out: Quad[] = []
    let n = 0
    while (n < members.length && !keeps.some((keep) => keep(n, members[n].at, size))) {
      const m = members[n++].event
      out.push(...store.match(this.stream, this.f.namedNode(LDES.member), m, this.graph), ...store.match(m, null, null, this.graph))
      // Its recorded change (for undo) goes with it.
      for (const p of [BEH.asserted, BEH.retracted]) for (const q of store.match(m, this.f.namedNode(p), null, this.graph)) out.push(...store.match(q.object as NamedNode, null, null, this.graph))
    }
    members.splice(0, n)
    return out
  }

  /** The retained members in append order, read from the graph once. */
  private members() {
    if (this.retained) return this.retained
    const store = this.runtime.store
    this.retained = store
      .match(this.stream, this.f.namedNode(LDES.member), null, this.graph)
      .map((m) => ({
        event: m.object as NamedNode,
        seq: Number(this.value(m.object, BEH.sequence)?.value ?? 0),
        at: Date.parse(this.value(m.object, BEH.at)?.value ?? '') || 0,
      }))
      .sort((a, b) => a.seq - b.seq)
      .map(({ event, at }) => ({ event, at }))
    return this.retained
  }

  /** Stream members, most recent first, with all their triples (walks back from the head). */
  recent(n = 5): Quad[][] {
    const store = this.runtime.store
    const out: Quad[][] = []
    for (let m = this.value(this.stream, BEH.latest); m && out.length < n; m = this.value(m, BEH.previous)) {
      const quads = store.match(m, null, null, this.graph)
      if (!quads.length) break
      out.push(quads)
    }
    return out
  }

  private react(r: Reaction, event: NamedNode, target: NamedNode): Delta {
    const useFn = r.fn && (!this.runtime.store.sparql || this.opts.prefer === 'function' || (!r.deletes && !r.inserts))
    if (useFn) {
      const fn = this.opts.functions?.[r.fn!] ?? builtinReactions[r.fn!]
      if (!fn) throw new Error(`No reaction function registered for "${r.fn}" (<${r.iri}>)`)
      return fn({ runtime: this.runtime, event, target, match: this.match, object: (s, p) => this.value(s, p) })
    }
    const run = (q: string | undefined) => {
      if (!q) return []
      const prefixes = prefixesFor(this.runtime.store)
      const text = (prefixes + '\n' + q).replaceAll('$event', `<${event.value}>`).replaceAll('$this', `<${target.value}>`)
      try {
        return this.runtime.store.construct(text, this.graphs)
      } catch (e) {
        throw new Error(`Reaction <${r.iri}> failed: ${(e as Error).message}`)
      }
    }
    return { remove: run(r.deletes), add: run(r.inserts) }
  }

  /**
   * Record DOM interactions inside a rendered projection. For each DOM event,
   * the innermost component with a matching reaction receives the RDF event.
   * Content mounted in the projection's host-owned slots (another projection, a
   * sandbox) is left to whoever mounted it. With `onError`, a reaction or rule that
   * throws while an event is handled is reported there instead of thrown (a sandbox
   * keeps its failures to itself).
   */
  attach(renderer: DomRenderer, handledBy: (h: Handled) => void = () => {}, onError?: (e: Error) => void): () => void {
    const domTypes = new Set(this.runtime.store.match(null, this.f.namedNode(BEH.domEvent), null).map((q) => q.object.value))
    const doc = renderer.host.ownerDocument
    // After every handled event (and the host's flush), the focus follows the modal overlays.
    const modals = new ModalFocus(renderer)
    const onHandled = (h: Handled) => {
      handledBy(h)
      modals.settle()
    }
    const apple = /Mac|iPhone|iPad/.test(doc.defaultView?.navigator?.platform ?? '')
    // Key presses a reaction handled are not also the keyboard layer's (nor the browser's).
    const consumed = new WeakSet<Event>()
    // The modifiers of the press under way: a focus it moves carries them (Shift+click extends).
    let pressed: string | undefined
    const press = (e: Event) => void (pressed = e.type === 'pointerdown' ? modifiersOf(e as PointerEvent, apple) : undefined)
    const guarded =
      <E extends Event>(handle: (e: E) => void) =>
      (e: E) => {
        if (!onError) return handle(e)
        try {
          handle(e)
        } catch (err) {
          onError(err as Error)
        }
      }
    const listener = guarded((e: Event) => {
      if (renderer.inSlot(e.target as Node)) return
      // Key events carry their combination; an event class may name the keys it is for (beh:key).
      const combo = e.type.startsWith('key') && (e as KeyboardEvent).key ? comboOf(e as KeyboardEvent, apple) : undefined
      const types = combo === undefined ? this.eventClasses(e.type) : this.eventClasses(e.type).filter((t) => this.forKey(t, combo))
      if (!types.length) return
      let node: string | undefined
      for (let el = e.target as Node | null; el && el !== renderer.host; el = el.parentNode) {
        const key = renderer.keyOf(el)
        node ??= key
        const focus = key && renderer.index.focusOf(key)
        if (!focus) continue
        for (const type of types) {
          const reactions = this.reactionsFor(type, focus)
          if (!reactions.length) continue
          const control = e.target as HTMLInputElement
          const detail: EventDetail = { node }
          if (e.type !== 'click' && e.type !== 'mouseover' && 'value' in control && typeof control.value === 'string') detail.value = control.value
          if (control.type === 'checkbox') detail.checked = control.checked
          if (detail.value !== undefined && typeof control.selectionStart === 'number') detail.caret = control.selectionStart
          if (combo !== undefined) detail.key = combo
          if (e.type === 'focusin') detail.modifiers = pressed
          else if (combo === undefined && 'shiftKey' in e) detail.modifiers = modifiersOf(e as MouseEvent, apple)
          const custom = (e as CustomEvent).detail
          if (custom && typeof custom === 'object') {
            // Numbers are beh:* integers (a measure the host could not take is left out); a string names a
            // rendered node (beh:onto: where a move was let go).
            detail.numbers = {}
            for (const [name, v] of Object.entries(custom as Record<string, unknown>)) {
              if (typeof v === 'number' && Number.isFinite(v)) detail.numbers[name] = v
              else if (typeof v === 'string') (detail.about ??= []).push([NS.beh + name, this.f.namedNode(v)])
            }
          }
          const before = this.focusAt(renderer, focus, e.target as Node)
          const handled = this.emit(type, focus, detail, reactions)
          // Reactions that changed nothing let the event reach the enclosing components
          // (a generic reaction on c:Button must not swallow its container's click).
          if (!handled.recorded && !handled.delta.add.length && !handled.delta.remove.length) continue
          if (combo !== undefined) e.preventDefault(), consumed.add(e)
          onHandled(handled)
          this.placeCaret(renderer, focus)
          this.placeFocus(renderer, focus, before)
          return
        }
      }
    })
    // Drag and drop (ontology/layers/dragdrop.ttl): the pointer gesture (below), and its keys before
    // any other key handling - on the host for keys inside it, on the document for the rest.
    const drags = new DragHost(this, renderer, onHandled)
    const dragKeys = (e: Event) => {
      if ((e.currentTarget === doc) === renderer.host.contains(e.target as Node)) return
      if (drags.key(e as KeyboardEvent, apple)) e.preventDefault(), e.stopImmediatePropagation()
    }
    const native = (e: Event) => (e.target as Element).closest?.('[data-sem-gesture=drag]') && e.preventDefault()
    renderer.host.addEventListener('keydown', dragKeys)
    doc.addEventListener('keydown', dragKeys)
    renderer.host.addEventListener('dragstart', native)
    // Pressing inside an element marked data-sem-keep-focus (a typeahead's list) leaves the focus
    // where it is, so the field keeps it while one of its options is clicked.
    const keepFocus = (e: Event) => (e.target as Element).closest?.('[data-sem-keep-focus]') && e.preventDefault()
    renderer.host.addEventListener('mousedown', keepFocus)
    for (const t of domTypes) renderer.host.addEventListener(t, listener)
    // A field marked data-sem-controlled shows what the graph says once its input is handled,
    // also when nothing changed (a code input drops characters its charset does not allow).
    const controlled = (e: Event) => {
      const field = e.target as HTMLInputElement
      const key = field.hasAttribute?.('data-sem-controlled') ? renderer.keyOf(field) : undefined
      if (!key) return
      const value = new Map(renderer.index.attributes(key)).get('value') ?? ''
      if (field.value !== value) field.value = value
    }
    renderer.host.addEventListener('input', controlled)
    const gestures = (e: Event) => renderer.inSlot(e.target as Node) || this.gesture(e as PointerEvent, renderer, drags)
    renderer.host.addEventListener('pointerdown', gestures)
    renderer.host.addEventListener('pointerdown', press, true)
    renderer.host.addEventListener('pointerup', press, true)
    // Keyboard layer: a key combination activates the element that declares it. A field it focuses
    // remembers where the focus was, to give it back on an Escape nothing else uses.
    const cameFrom = new WeakMap<Element, HTMLElement | null>()
    const keys = guarded((e: Event) => {
      const k = e as KeyboardEvent
      if (!k.key || consumed.has(k) || ['Shift', 'Control', 'Alt', 'Meta'].includes(k.key)) return
      // An open modal dialog keeps the focus: Tab goes round inside it.
      if (trapTab(k, renderer.host)) return k.preventDefault()
      // Keys in slot content are the mounted projection's own (its bus navigates it).
      if (renderer.host.contains(k.target as Node) && !renderer.inSlot(k.target as Node) && (sizeByKey(k, renderer) || moveByKey(k, renderer) || navigate(k))) return k.preventDefault()
      // Tab stays inside an open modal overlay.
      if (modals.tab(k)) return k.preventDefault()
      const combo = comboOf(k, apple)
      const typing = (k.target as Element | null)?.closest?.('input, textarea, select, [contenteditable]')
      if (typing && !combo.startsWith('mod+') && k.key !== 'Escape') return
      // An Escape nothing used in a field a shortcut focused: back to where the focus was.
      const back = typing && combo === 'escape' ? cameFrom.get(typing) : undefined
      if (typing && back !== undefined) {
        k.preventDefault()
        cameFrom.delete(typing)
        if (back?.isConnected && back !== doc.body) back.focus?.()
        else (typing as HTMLElement).blur?.()
        return
      }
      // Commands (key:Command) are graph nodes, not elements: the press becomes their event,
      // carrying where the pointer is (the node under it, its distance to the edges).
      const command = this.commandFor(combo)
      if (command && !(typing && this.value(this.f.namedNode(command), KEY.whileTyping)?.value === 'false')) {
        k.preventDefault()
        const handled = this.emit(KEY.Press, command, this.pointerDetail(renderer))
        if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) onHandled(handled)
        return
      }
      // Horizontal arrows follow the reading direction: in a right-to-left page, ArrowLeft is "next".
      const flipped = mirrored(combo)
      const el = [...renderer.host.querySelectorAll('[data-sem-shortcut]')].find((x) => {
        const own = x.getAttribute('data-sem-shortcut')
        return (own === combo ? flipped === combo || !rightToLeft(x) : flipped !== combo && own === flipped && rightToLeft(x)) && !renderer.inSlot(x)
      }) as HTMLElement | undefined
      if (!el && combo === 'escape') {
        // Escape dismisses the most recent open overlay: its control marked data-sem-escape.
        const dismiss = [...renderer.host.querySelectorAll('[data-sem-escape]')].filter((x) => !x.closest('[hidden]') && !renderer.inSlot(x)).at(-1) as HTMLElement | undefined
        if (!dismiss) return
        k.preventDefault()
        // A disclosure's toggle (aria-expanded) takes the focus back from what it closes.
        if (dismiss.hasAttribute('aria-expanded')) dismiss.focus?.()
        dismiss.click()
        return
      }
      if (!el || el.closest('[hidden], [inert]')) return
      k.preventDefault()
      const field = el.matches('input, textarea, select') ? el : (el.querySelector('input:not([type=checkbox]):not([type=radio]), textarea, select') as HTMLElement | null)
      if (field) {
        const from = doc.activeElement as HTMLElement | null
        field.focus()
        ;(field as HTMLInputElement).select?.()
        if (from !== field) {
          cameFrom.set(field, from)
          field.addEventListener('focusout', () => cameFrom.delete(field), { once: true })
        }
        return
      }
      const control = el.matches('button, a, [role=button], input') ? el : (el.querySelector('button, a, [role=button], input') as HTMLElement | null)
      ;(control ?? el).click()
    })
    doc.addEventListener('keydown', keys)
    // A press outside an open disclosure that asks for it (data-sem-dismiss-outside on its toggle)
    // closes it: the toggle is clicked, so its own reactions do it, as Escape does above.
    const outside = (e: Event) => {
      const target = e.target as Node
      for (const toggle of renderer.host.querySelectorAll<HTMLElement>('[data-sem-dismiss-outside]')) {
        if (toggle.closest('[hidden]')) continue
        const region = toggle.getAttribute('aria-controls')
        const panel = region ? doc.getElementById(region) : null
        if (toggle.contains(target) || panel?.contains(target)) continue
        toggle.click()
      }
    }
    doc.addEventListener('pointerdown', outside, true)
    const track = (e: Event) => {
      const p = e as PointerEvent
      this.pointer = { x: p.clientX, y: p.clientY }
    }
    doc.addEventListener('pointermove', track, { passive: true })
    // Entering a navigable component by Tab puts the rest of its parts out of the tab order.
    const entered = (e: Event) => {
      const part = e.target as HTMLElement
      if (renderer.inSlot(part)) return
      const root = part.closest?.('[data-sem-nav-items]')
      if (root && part.matches(root.getAttribute('data-sem-nav-items')!)) rove(root, part)
    }
    renderer.host.addEventListener('focusin', entered)
    return () => {
      for (const t of domTypes) renderer.host.removeEventListener(t, listener)
      renderer.host.removeEventListener('input', controlled)
      renderer.host.removeEventListener('pointerdown', gestures)
      renderer.host.removeEventListener('pointerdown', press, true)
      renderer.host.removeEventListener('pointerup', press, true)
      renderer.host.removeEventListener('keydown', dragKeys)
      doc.removeEventListener('keydown', dragKeys)
      renderer.host.removeEventListener('dragstart', native)
      renderer.host.removeEventListener('mousedown', keepFocus)
      drags.dispose()
      doc.removeEventListener('keydown', keys)
      doc.removeEventListener('pointerdown', outside, true)
      doc.removeEventListener('pointermove', track)
      renderer.host.removeEventListener('focusin', entered)
    }
  }

  /** Whether an event class is recorded for a key combination: any key, unless it names some (beh:key). */
  private forKey(type: string, key: string): boolean {
    const keys = this.runtime.store.match(this.f.namedNode(type), this.f.namedNode(NS.beh + 'key'), null, this.f.defaultGraph())
    return !keys.length || keys.some((q) => q.object.value === key)
  }

  /**
   * A text control rendered with data-sem-caret keeps its caret where the graph says
   * after an event its component handled (rewriting the value would put it at the end).
   */
  private placeCaret(renderer: DomRenderer, focus: string) {
    const root = renderer.nodeOf(focus + MINT_SEPARATOR + 'root') as HTMLElement | undefined
    // Only the component's own control (not one of a component nested in it).
    const field = [...(root?.querySelectorAll?.('[data-sem-caret]') ?? [])].find((el) => owningFocus(renderer.keyOf(el) ?? '') === focus) as
      | HTMLTextAreaElement
      | undefined
    const at = Number(field?.getAttribute('data-sem-caret'))
    if (!field || !Number.isFinite(at) || typeof field.setSelectionRange !== 'function') return
    if (field.ownerDocument.activeElement !== field) field.focus?.()
    if (field.selectionStart !== at || field.selectionEnd !== at) field.setSelectionRange(at, at)
  }

  /** Where the focus is as an event reaches a component: in it or not, and the elements around it. */
  private focusAt(renderer: DomRenderer, focus: string, target: Node) {
    const doc = target.ownerDocument
    const active = doc?.activeElement ?? null
    const root = renderer.nodeOf(focus + MINT_SEPARATOR + 'root') as HTMLElement | undefined
    const around: Element[] = []
    for (let el = active?.parentElement; el && el !== renderer.host; el = el.parentElement) around.push(el)
    return { active, inside: !!active && active !== doc?.body && !!root?.contains(active), around }
  }

  /**
   * After an event a component handled, the element its rendering marks data-sem-takes-focus (a
   * month grid's active day, a date field's button once its popup closed) gets the focus, if the
   * focus was in the component when the event came: unless it is in a text field (typing keeps it).
   * If the focused element went away or was hidden, the nearest marked element around where it was
   * wins. An input whose aria-activedescendant names an option keeps that option in view.
   */
  private placeFocus(renderer: DomRenderer, focus: string, at: ReturnType<EventBus['focusAt']>) {
    const root = renderer.nodeOf(focus + MINT_SEPARATOR + 'root') as HTMLElement | undefined
    for (const field of root?.querySelectorAll?.<HTMLElement>('[aria-activedescendant]') ?? []) {
      field.ownerDocument.getElementById(field.getAttribute('aria-activedescendant')!)?.scrollIntoView?.({ block: 'nearest' })
    }
    const { active, inside, around } = at
    if (!active || !inside) return
    const shown = (el: Element) => el.isConnected && !el.closest('[hidden], [inert]')
    const lost = !shown(active)
    if (!lost && active.matches('input:not([type=checkbox]):not([type=radio]):not([type=button]), textarea, [contenteditable]')) return
    const marked = (scope: Element) => [...scope.querySelectorAll<HTMLElement>('[data-sem-takes-focus]')].find(shown)
    let target = root?.isConnected ? marked(root) : undefined
    if (!target && lost) for (const scope of around) if (scope.isConnected && (target = marked(scope))) break
    if (target && target !== target.ownerDocument.activeElement) target.focus?.()
  }

  /**
   * Drag gestures on handles marked data-sem-gesture: the nearest component
   * root follows the pointer (a transient view change), and on release one
   * sem-move / sem-resize DOM event carries the result into an RDF event. A "drag" handle
   * carries what it shows to drop targets (src/behaviour/drag.ts).
   *
   * A move says where the box was let go (x, y), how far the pointer was from each edge of the
   * viewport, and how fast it was going then (inlineVelocity, blockVelocity, px/s): the graph
   * decides whether that docks it (c:snapDistance, c:throwTime). A handle marked
   * data-sem-move-size="content" carries its box at the box's own size, floating; any other is
   * pinned to the size it had, so a frame attached to an edge comes away whole.
   */
  private gesture(e: PointerEvent, renderer: DomRenderer, drags: DragHost) {
    const handle = (e.target as Element).closest?.('[data-sem-gesture]') as HTMLElement | null
    // A detachable tab is a button that also tears off (src/behaviour/tear.ts).
    if (handle?.dataset.semGesture === 'tear') return tearGesture(e, handle)
    if (!handle || e.button !== 0 || (e.target as Element).closest('button, input, select, textarea')) return
    if (handle.dataset.semGesture === 'size') return this.sizeGesture(e, handle, renderer)
    if (handle.dataset.semGesture === 'drag') return drags.pointer(e, handle)
    const box = boxOf(handle, renderer)
    if (!box) return
    const kind = handle.dataset.semGesture
    // Where the box is on screen (a theme may centre it with a translate, which the drag drops).
    const rect = box.getBoundingClientRect()
    const target = box
    // A box placed in a container (a canvas's item), not on the screen: its left and top are in the
    // container's padding box, and one in flow comes out of it while dragged.
    const position = kind === 'move' ? target.ownerDocument.defaultView?.getComputedStyle?.(target)?.position : undefined
    const inFlow = position === 'static' || position === 'relative'
    const parent = kind === 'move' && position !== 'fixed' ? (target.offsetParent as HTMLElement | null) : null
    const origin = parent ? parent.getBoundingClientRect() : undefined
    const off = origin && parent ? { x: origin.left + parent.clientLeft - parent.scrollLeft, y: origin.top + parent.clientTop - parent.scrollTop } : { x: 0, y: 0 }
    const start = { x: e.clientX, y: e.clientY, left: rect.left - off.x, top: rect.top - off.y, width: box.offsetWidth, height: box.offsetHeight }
    const natural = kind === 'move' && handle.dataset.semMoveSize === 'content'
    // What the graph says (the renderer's last write); the drag only changes the view until release.
    const original = target.getAttribute('style')
    const edge = target.getAttribute('data-sem-edge')
    // A window torn from a tab can be let go over that tab list again: then the move is onto it.
    const frame = kind === 'move' ? renderer.index.focusOf(renderer.keyOf(box)!) : undefined
    const home = frame ? returnPlaces(this.runtime.store, this.projection.engine.graph, renderer, box, frame) : undefined
    e.preventDefault()
    handle.setPointerCapture?.(e.pointerId)
    // The last moments of the gesture, for its velocity at release.
    const trail: { x: number; y: number; t: number }[] = [{ x: e.clientX, y: e.clientY, t: e.timeStamp }]
    let moved = false
    const move = (m: PointerEvent) => {
      const dx = m.clientX - start.x
      const dy = m.clientY - start.y
      trail.push({ x: m.clientX, y: m.clientY, t: m.timeStamp })
      while (trail.length > 2 && m.timeStamp - trail[0]!.t > 100) trail.shift()
      if (kind === 'move') {
        if (!moved && natural) target.setAttribute('data-sem-edge', 'none')
        moved = true
        const size = natural ? {} : { width: `${start.width}px`, height: `${start.height}px` }
        // Grabbed docked, it comes away at its own size under the pointer, as far into it as it was.
        const width = natural ? target.offsetWidth : start.width
        const height = natural ? target.offsetHeight : start.height
        const left = natural ? m.clientX - off.x - Math.min(start.x - off.x - start.left, width / 2) : start.left + dx
        const top = natural ? m.clientY - off.y - Math.min(start.y - off.y - start.top, height / 2) : start.top + dy
        Object.assign(target.style, { left: `${left}px`, top: `${Math.max(0, top)}px`, right: 'auto', bottom: 'auto', translate: 'none', ...size, ...(inFlow ? { position: 'absolute', margin: '0' } : {}) })
        home?.mark(home.at(m.clientX, m.clientY)?.el)
      } else Object.assign(target.style, { width: `${Math.max(280, start.width + dx)}px`, height: `${Math.max(180, start.height + dy)}px` })
    }
    const up = (u: PointerEvent) => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', up)
      handle.removeEventListener('pointercancel', up)
      let detail: Record<string, number | string> | undefined
      const onto = kind === 'move' && moved && u.type === 'pointerup' ? home?.at(u.clientX, u.clientY) : undefined
      home?.mark(undefined)
      // Let go over the tab list it came from: a move onto it, with no position (the window keeps its own).
      if (onto) detail = { onto: onto.key }
      else if (kind === 'move' && moved && u.type === 'pointerup') {
        // Where the pointer was released, as distances to the viewport's logical edges (for snapping).
        const view = target.ownerDocument.documentElement
        const rtl = target.ownerDocument.defaultView?.getComputedStyle?.(target).direction === 'rtl'
        const fromLeft = u.clientX
        const fromRight = view.clientWidth - u.clientX
        // Its velocity over the last 100 ms (none if it came to rest before it was let go).
        const first = trail[0]!
        const span = u.timeStamp - first.t
        const still = u.timeStamp - trail[trail.length - 1]!.t > 60
        const vx = span > 0 && !still ? ((u.clientX - first.x) / span) * 1000 : 0
        const vy = span > 0 && !still ? ((u.clientY - first.y) / span) * 1000 : 0
        const placed = target.getBoundingClientRect()
        detail = {
          x: natural ? placed.left : target.offsetLeft,
          y: natural ? placed.top : target.offsetTop,
          toStart: rtl ? fromRight : fromLeft,
          toEnd: rtl ? fromLeft : fromRight,
          toTop: u.clientY,
          toBottom: view.clientHeight - u.clientY,
          inlineVelocity: rtl ? -vx : vx,
          blockVelocity: vy,
        }
      } else if (kind !== 'move') detail = { width: target.offsetWidth, height: target.offsetHeight }
      // Back to the graph's state: the reactions decide what the gesture did (moved, snapped
      // to an edge, or nothing), and the renderer shows that.
      if (original === null) target.removeAttribute('style')
      else target.setAttribute('style', original)
      if (edge === null) target.removeAttribute('data-sem-edge')
      else target.setAttribute('data-sem-edge', edge)
      // A press that never moved (or a gesture the browser took over) changes nothing.
      // Made by the handle's own window (a test DOM refuses foreign events).
      const Custom = handle.ownerDocument.defaultView?.CustomEvent ?? CustomEvent
      if (detail) handle.dispatchEvent(new Custom(kind === 'move' ? 'sem-move' : 'sem-resize', { bubbles: true, detail }))
    }
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', up)
    handle.addEventListener('pointercancel', up)
  }

  /**
   * One-axis sizing (data-sem-gesture="size": a separator, a column's edge, a panel's free
   * edge). The handle says what it sizes (data-sem-sizes: "previous" or "next" for a sibling,
   * else a selector for its closest ancestor, else the nearest component root), along which
   * axis (data-sem-axis "x" or "y"), in which direction (data-sem-sign -1 when dragging toward
   * the start grows it) and within which bounds (data-sem-min, data-sem-max, in px). While
   * dragging only the view changes; on release one sem-size DOM event carries the new size
   * and the room there was (the sized element's parent along the axis) into a beh:Size event.
   */
  private sizeGesture(e: PointerEvent, handle: HTMLElement, renderer: DomRenderer) {
    const target = sizedBy(handle, renderer)
    if (!target) return
    const { axis, sign, min, max } = sizing(handle)
    const start = { at: axis === 'x' ? e.clientX : e.clientY, size: measure(target, axis) }
    const original = target.getAttribute('style')
    e.preventDefault()
    handle.setPointerCapture?.(e.pointerId)
    let size = start.size
    const move = (m: PointerEvent) => {
      size = Math.min(max, Math.max(min, start.size + sign * ((axis === 'x' ? m.clientX : m.clientY) - start.at)))
      setSize(target, axis, size)
    }
    const up = () => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', up)
      handle.removeEventListener('pointercancel', up)
      if (original === null) target.removeAttribute('style')
      else target.setAttribute('style', original)
      if (size !== start.size) handle.dispatchEvent(sizeEvent(handle, size, extentOf(target, axis)))
    }
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', up)
    handle.addEventListener('pointercancel', up)
  }
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type=hidden]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * The WAI-ARIA dialog pattern's focus, as view state: when a modal overlay (aria-modal="true")
 * opens, the focus moves into it (to its [autofocus] element, else the panel itself) and Tab keeps
 * it there; when it closes, the focus goes back to where it was before it opened, unless something
 * else took it meanwhile (the renderer moving it off a removed panel does not count). Nested modals stack. Nothing here is graph state.
 */
class ModalFocus {
  private readonly open: { panel: HTMLElement; back: Element | null }[] = []

  constructor(private readonly renderer: DomRenderer) {}

  private shown = (el: Element) => el.isConnected && !el.closest('[hidden], [inert]') && !this.renderer.inSlot(el)

  /** Follow what the last flush opened and closed. */
  settle(): void {
    const host = this.renderer.host
    const doc = host.ownerDocument
    const now = [...host.querySelectorAll<HTMLElement>('[aria-modal="true"]')].filter(this.shown)
    for (let i = this.open.length - 1; i >= 0; i--) {
      const { panel, back } = this.open[i]!
      if (now.includes(panel)) continue
      this.open.splice(i, 1)
      const active = doc.activeElement
      // The renderer's guess at a nearby control, when the panel went with the focus in it, is not a choice.
      const lost = !active || active === doc.body || !active.isConnected || panel.contains(active) || active === this.renderer.refocused
      if (lost && back?.isConnected && this.shown(back)) (back as HTMLElement).focus?.({ preventScroll: true })
    }
    for (const panel of now) {
      if (this.open.some((m) => m.panel === panel)) continue
      this.open.push({ panel, back: doc.activeElement })
      const first = panel.querySelector<HTMLElement>('[autofocus]') ?? panel
      if (!panel.contains(doc.activeElement)) first.focus?.({ preventScroll: true })
    }
  }

  /** Tab and Shift+Tab cycle through the topmost modal's controls. Returns whether the key was used. */
  tab(k: KeyboardEvent): boolean {
    const top = this.open.at(-1)?.panel
    if (k.key !== 'Tab' || !top || k.altKey || k.ctrlKey || k.metaKey || !this.shown(top)) return false
    const parts = [...top.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(this.shown)
    const active = top.ownerDocument.activeElement
    if (!parts.length) return top.focus?.(), true
    const at = parts.indexOf(active as HTMLElement)
    if (at < 0) return parts[0]!.focus?.(), true
    if (k.shiftKey && at === 0) return parts.at(-1)!.focus?.(), true
    if (!k.shiftKey && at === parts.length - 1) return parts[0]!.focus?.(), true
    return false
  }
}

/** The component root a gesture handle belongs to (the nearest element that renders a focus). */
function boxOf(handle: HTMLElement, renderer: DomRenderer): HTMLElement | null {
  let box: HTMLElement | null = handle
  while (box && !(renderer.keyOf(box) && renderer.index.focusOf(renderer.keyOf(box)!))) box = box.parentElement
  return box
}

// --- moving by key (move handles) -------------------------------------------------------------------

/** The edge each arrow key heads for, by writing direction. */
const ARROW_EDGES: Record<string, { ltr: string; rtl: string }> = {
  ArrowUp: { ltr: 'top', rtl: 'top' },
  ArrowDown: { ltr: 'bottom', rtl: 'bottom' },
  ArrowLeft: { ltr: 'start', rtl: 'end' },
  ArrowRight: { ltr: 'end', rtl: 'start' },
}
const OPPOSITE: Record<string, string> = { top: 'bottom', bottom: 'top', start: 'end', end: 'start' }

/**
 * Arrow keys on a focused move handle (data-sem-gesture="move" that takes focus): 16 px a press
 * (64 with Shift), kept on screen, committed like a drag with one sem-move event that says where
 * the box is and how far it now is from the edge it moved towards, so the graph docks it there once
 * it is within reach (c:snapDistance). Docked, the arrow away from its edge takes it off (a move
 * with no position: it floats where it last floated); the others do nothing. Returns whether the
 * key was used.
 */
export function moveByKey(k: KeyboardEvent, renderer: DomRenderer): boolean {
  const handle = (k.target as Element | null)?.closest?.('[data-sem-gesture="move"]') as HTMLElement | null
  const heading = ARROW_EDGES[k.key]
  if (!handle || !heading || !handle.hasAttribute('tabindex') || !renderer.host.contains(handle) || k.altKey || k.ctrlKey || k.metaKey) return false
  const box = boxOf(handle, renderer)
  if (!box) return false
  const doc = box.ownerDocument
  const rtl = doc.defaultView?.getComputedStyle?.(box)?.direction === 'rtl'
  const towards = rtl ? heading.rtl : heading.ltr
  const edge = box.getAttribute('data-sem-edge')
  const send = (detail: Record<string, number>) => handle.dispatchEvent(new (doc.defaultView?.CustomEvent ?? CustomEvent)('sem-move', { bubbles: true, detail }))
  if (edge && edge !== 'none') {
    if (towards === OPPOSITE[edge]) send({})
    return true
  }
  const view = doc.documentElement
  const r = box.getBoundingClientRect()
  const step = k.shiftKey ? 64 : 16
  const dx = k.key === 'ArrowLeft' ? -step : k.key === 'ArrowRight' ? step : 0
  const dy = k.key === 'ArrowUp' ? -step : k.key === 'ArrowDown' ? step : 0
  const left = Math.max(0, Math.min(view.clientWidth - r.width, r.left + dx))
  const top = Math.max(0, Math.min(view.clientHeight - r.height, r.top + dy))
  const away = { top, bottom: view.clientHeight - (top + r.height), start: rtl ? view.clientWidth - (left + r.width) : left, end: rtl ? left : view.clientWidth - (left + r.width) }
  const name = { top: 'toTop', bottom: 'toBottom', start: 'toStart', end: 'toEnd' }[towards]!
  send({ x: left, y: top, [name]: away[towards as keyof typeof away] })
  return true
}

// --- one-axis sizing (separators) ------------------------------------------------------------------

/** The element a size handle sizes (see EventBus.sizeGesture). */
function sizedBy(handle: HTMLElement, renderer: DomRenderer): HTMLElement | null {
  const sizes = handle.dataset.semSizes
  if (sizes === 'previous') return handle.previousElementSibling as HTMLElement | null
  if (sizes === 'next') return handle.nextElementSibling as HTMLElement | null
  if (sizes) return handle.closest(sizes) as HTMLElement | null
  let box: HTMLElement | null = handle
  while (box && !(renderer.keyOf(box) && renderer.index.focusOf(renderer.keyOf(box)!))) box = box.parentElement
  return box
}

function sizing(handle: HTMLElement) {
  const n = (v: string | undefined, fallback: number) => (v !== undefined && Number.isFinite(Number(v)) ? Number(v) : fallback)
  return {
    axis: handle.dataset.semAxis === 'y' ? ('y' as const) : ('x' as const),
    sign: n(handle.dataset.semSign, 1) < 0 ? -1 : 1,
    min: n(handle.dataset.semMin, 0),
    max: n(handle.dataset.semMax, Infinity),
    step: n(handle.dataset.semStep, 16),
  }
}

const measure = (el: HTMLElement, axis: 'x' | 'y') => {
  const r = el.getBoundingClientRect?.()
  return (axis === 'x' ? (r?.width ?? el.offsetWidth) : (r?.height ?? el.offsetHeight)) || 0
}
const extentOf = (el: HTMLElement, axis: 'x' | 'y') => (el.parentElement ? measure(el.parentElement, axis) : 0)

/** The sem-size DOM event, made by the handle's own window (a test DOM refuses foreign events). */
const sizeEvent = (handle: HTMLElement, size: number, extent: number) =>
  new (handle.ownerDocument.defaultView?.CustomEvent ?? CustomEvent)('sem-size', { bubbles: true, detail: { size, extent } })

/** The transient view of a size being dragged: pinned along its axis (flex items too). */
function setSize(el: HTMLElement, axis: 'x' | 'y', size: number) {
  Object.assign(el.style, axis === 'x' ? { width: `${size}px`, flex: `0 0 ${size}px` } : { height: `${size}px`, flex: `0 0 ${size}px` })
}

/**
 * Arrow keys on a focused size handle (role=separator): a step along its axis (data-sem-step,
 * 16px by default), Home and End to its bounds. Commits like a drag, with one sem-size event.
 * Returns whether the key was used.
 */
export function sizeByKey(k: KeyboardEvent, renderer: DomRenderer): boolean {
  const handle = (k.target as Element | null)?.closest?.('[data-sem-gesture="size"]') as HTMLElement | null
  if (!handle || !renderer.host.contains(handle) || k.altKey || k.ctrlKey || k.metaKey) return false
  const { axis, sign, min, max, step } = sizing(handle)
  const forward = axis === 'x' ? 'ArrowRight' : 'ArrowDown'
  const back = axis === 'x' ? 'ArrowLeft' : 'ArrowUp'
  if (![forward, back, 'Home', 'End'].includes(k.key)) return false
  const target = sizedBy(handle, renderer)
  if (!target) return false
  const current = measure(target, axis)
  const size =
    k.key === 'Home' ? min : k.key === 'End' ? (Number.isFinite(max) ? max : current) : Math.min(max, Math.max(min, current + sign * (k.key === forward ? step : -step)))
  if (size !== current) handle.dispatchEvent(sizeEvent(handle, size, extentOf(target, axis)))
  return true
}
