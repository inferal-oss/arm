// The islands effect (ontology/layers/foreign.ttl, docs/FOREIGN.md): foreign components - custom
// elements, framework components, vanilla widgets - mounted in the host-owned slots that islands
// render. The graph says what to mount and with which inputs (derived on the slot by the island's
// rendering, so an input changes only when its triples do); the effect mounts it through the adapter
// of its kind once its slot is in the page, pushes each changed input, and turns the outputs it
// declares into events addressed to the island, which ordinary reactions turn into state.
//
// Echo suppression: an input is pushed only when its value differs from the last one pushed, and
// adapters skip a value the component already has, so the component's own change coming back through
// the graph is not written again (a caret stays where it is). An output raised while inputs are being
// pushed to its island is the component echoing what it was told, and is dropped. The graph is the
// source of truth: a reaction that changes the value it was told (clamps, refuses) is pushed back.
import type { Term } from '@rdfjs/types'
import type { EventBus, Handled } from './behaviour/events.ts'
import { EXT, NS, RDFS_SUBCLASS, RDF_TYPE } from './ns.ts'
import type { DomRenderer } from './render/dom.ts'
import type { IslandInput } from './render/html-index.ts'

const XSD = NS.xsd
const RDF_JSON = NS.rdf + 'JSON'
const NUMERIC = new Set(['integer', 'decimal', 'double', 'float', 'int', 'long', 'short', 'nonNegativeInteger', 'positiveInteger'].map((t) => XSD + t))

/** An input as a component receives it. */
export interface Input {
  name: string
  /** Numbers, booleans and rdf:JSON values arrive typed; IRIs and other literals as strings. */
  value: unknown
  /** EXT.Property or EXT.Attribute. */
  as: string
}

/** What an adapter is given to mount one island. */
export interface IslandContext {
  /** The island (the component instance). */
  island: string
  /** Its ext:tag and ext:widget, if it says them. */
  tag?: string
  widget?: string
  /** Every input's current value. */
  inputs: ReadonlyMap<string, Input>
  /** The names of the outputs the island declares (ext:on): DOM event types or callback names. */
  outputs: readonly string[]
  /** Report an output by its name; its payload is read along the binding's ext:path and ext:field. */
  emit(name: string, payload: unknown): void
  /** The widgets registered with the effect (for the widget adapter). */
  widgets: Readonly<Record<string, WidgetLike>>
}

/** A mounted foreign component. */
export interface IslandHandle {
  /** Inputs whose value changed since the last push. */
  update(changed: ReadonlyMap<string, Input>): void
  destroy(): void
}

/** Mounts one kind of foreign component in a slot (registered by its ext:Adapter IRI). */
export type IslandAdapter = (slot: Element, ctx: IslandContext) => IslandHandle | Promise<IslandHandle>

/** A vanilla widget, or a framework root behind the same three calls (see docs/FOREIGN.md for React). */
export interface Widget {
  mount(element: HTMLElement, inputs: Record<string, unknown>, emit: (name: string, payload: unknown) => void): {
    update(changed: Record<string, unknown>): void
    destroy(): void
  }
}
/** A widget, or how to load it the first time an island needs it (a dynamic import). */
export type WidgetLike = Widget | { load(): Promise<Widget> }

export interface IslandsOptions {
  /** Adapters beyond the built-in ones (ext:CustomElement, ext:Widget), by their IRI. */
  adapters?: Record<string, IslandAdapter>
  /** Widgets by the name ext:widget gives. */
  widgets?: Record<string, WidgetLike>
  /**
   * Loaders by the name ext:module gives (a custom element's definition, a widget's library: a
   * dynamic import, from a CDN or a bundle). Each runs once, before the first island that names it
   * mounts. The graph names a module; only the host decides what code that is.
   */
  modules?: Record<string, () => Promise<unknown>>
  /** After each output a reaction handled (to patch the DOM). */
  onHandled?: (h: Handled) => void
  onError?: (e: Error, island: string) => void
}

interface OutputBinding {
  on: string
  emits: string
  path?: string
  fields: { path: string; predicate: string }[]
}

interface Mounted {
  key: string
  island: string
  el: Element
  handle?: IslandHandle
  /** Inputs as last pushed (or waiting for an adapter that loads), by name, keyed by term. */
  pushed: Map<string, { key: string; as: string }>
  outputs: OutputBinding[]
  applying: boolean
  gone: boolean
}

/** A term as a JS value: numbers, booleans and JSON typed, everything else its lexical form. */
export function inputValue(t: Term): unknown {
  if (t.termType !== 'Literal') return t.value
  const dt = t.datatype?.value
  if (dt && NUMERIC.has(dt)) return Number(t.value)
  if (dt === XSD + 'boolean') return t.value === 'true' || t.value === '1'
  if (dt === RDF_JSON) {
    try {
      return JSON.parse(t.value)
    } catch {
      return t.value
    }
  }
  return t.value
}

const termKey = (t: Term) => `${t.termType}|${t.value}|${t.termType === 'Literal' ? (t.language || t.datatype?.value) : ''}`

const resolved = (i: IslandInput): Input => ({ name: i.name, value: inputValue(i.value), as: i.as })

/** The value at a dotted path ("detail.value") of a payload. */
function at(payload: unknown, path: string | undefined): unknown {
  if (!path) return payload
  let v: unknown = payload
  for (const part of path.split('.')) v = v == null ? undefined : (v as Record<string, unknown>)[part]
  return v
}

/** Set inputs on an element: attributes as strings, properties as values, skipping what it already has. */
export function applyInputs(el: Element, inputs: Iterable<Input>): void {
  for (const i of inputs) {
    if (i.as === EXT.Attribute) {
      if (i.value === undefined || i.value === null || i.value === false) el.hasAttribute(i.name) && el.removeAttribute(i.name)
      else {
        const s = i.value === true ? '' : typeof i.value === 'object' ? JSON.stringify(i.value) : String(i.value)
        if (el.getAttribute(i.name) !== s) el.setAttribute(i.name, s)
      }
    } else {
      const props = el as unknown as Record<string, unknown>
      const now = props[i.name]
      const same = typeof i.value === 'object' && i.value !== null ? JSON.stringify(now) === JSON.stringify(i.value) : Object.is(now, i.value)
      if (!same) props[i.name] = i.value
    }
  }
}

/** ext:CustomElement: the element named by ext:tag; inputs are its properties or attributes, outputs its DOM events. */
export const customElementAdapter: IslandAdapter = (slot, ctx) => {
  if (!ctx.tag || !ctx.tag.includes('-')) throw new Error(`<${ctx.island}> names no custom element tag (ext:tag)`)
  const el = slot.ownerDocument.createElement(ctx.tag)
  applyInputs(el, ctx.inputs.values())
  const listeners = ctx.outputs.map((name) => [name, (e: Event) => ctx.emit(name, e)] as const)
  for (const [name, l] of listeners) el.addEventListener(name, l)
  slot.replaceChildren(el)
  return {
    update: (changed) => applyInputs(el, changed.values()),
    destroy() {
      for (const [name, l] of listeners) el.removeEventListener(name, l)
      el.remove()
    },
  }
}

/** ext:Widget: the widget registered under ext:widget, mounted in an element of its own in the slot. */
export const widgetAdapter: IslandAdapter = async (slot, ctx) => {
  const registered = ctx.widget !== undefined ? ctx.widgets[ctx.widget] : undefined
  if (!registered) throw new Error(`<${ctx.island}> names no registered widget (ext:widget ${ctx.widget ?? '-'})`)
  const widget = 'mount' in registered ? registered : await registered.load()
  const host = slot.ownerDocument.createElement('div')
  host.className = 'sem-island-widget'
  slot.replaceChildren(host)
  const values = (m: ReadonlyMap<string, Input>) => Object.fromEntries([...m.values()].map((i) => [i.name, i.value]))
  const h = widget.mount(host as HTMLElement, values(ctx.inputs), ctx.emit)
  return {
    update: (changed) => h.update(values(changed)),
    destroy() {
      h.destroy()
      host.remove()
    },
  }
}

export class Islands {
  private readonly mounted = new Map<string, Mounted>()
  private take?: () => { islands: Set<string>; recreate: Set<string> }
  private off?: () => void
  private seenVisibility = -1
  private syncing = false
  private readonly loading = new Set<Promise<unknown>>()
  private readonly loaded = new Map<string, Promise<unknown>>()
  /** Mounts, updates and outputs so far, for tests and cost checks. */
  readonly counts = { mounts: 0, updates: 0, destroys: 0, outputs: 0, echoes: 0 }

  constructor(
    readonly bus: EventBus,
    readonly renderer: DomRenderer,
    private readonly opts: IslandsOptions = {},
  ) {}

  /** Mount islands as they are rendered, from now on. Returns a function that stops it (and destroys them). */
  attach(): () => void {
    this.take = this.renderer.index.consumer()
    this.off = this.renderer.onPatched(() => this.sync())
    this.sync()
    return () => this.dispose()
  }

  dispose(): void {
    this.off?.()
    this.off = undefined
    for (const m of this.mounted.values()) this.destroy(m)
    this.mounted.clear()
  }

  /** How many islands are mounted. */
  get size(): number {
    return this.mounted.size
  }

  /** Resolves once every island whose adapter loads has mounted (for tests). */
  async settled(): Promise<void> {
    while (this.loading.size) await Promise.allSettled([...this.loading])
  }

  /** Mount what was rendered, push what changed, destroy what left (called after each patch). */
  sync(): void {
    if (this.syncing || !this.take) return
    this.syncing = true
    try {
      const index = this.renderer.index
      const d = this.take()
      const keys = new Set(d.islands)
      for (const k of d.recreate) if (this.mounted.has(k)) keys.add(k)
      // Something that decides what is in the page changed: an island may have been shown or dropped.
      if (index.visibility !== this.seenVisibility) {
        this.seenVisibility = index.visibility
        for (const k of index.islandSlots) keys.add(k)
        for (const k of this.mounted.keys()) keys.add(k)
      }
      for (const k of keys) this.reconcile(k)
    } finally {
      this.syncing = false
    }
  }

  private reconcile(key: string) {
    const index = this.renderer.index
    const island = index.nodes.get(key)?.island
    const el = this.renderer.nodeOf(key) as Element | undefined
    let m = this.mounted.get(key)
    if (m && (!island || m.el !== el || m.island !== island)) {
      this.destroy(m)
      this.mounted.delete(key)
      m = undefined
    }
    if (!island || !el) return
    if (!m) {
      // Mounted once it is in the page; a detached one keeps its state until its island leaves.
      if (el.isConnected) this.mount(key, island, el)
      return
    }
    if (m.handle) this.push(m)
  }

  private mount(key: string, island: string, el: Element) {
    const store = this.bus.runtime.store
    const f = store.factory
    const dg = f.defaultGraph()
    const classes = this.classesOf(island)
    const said = (p: string) => {
      for (const s of [island, ...classes]) {
        const o = store.match(f.namedNode(s), f.namedNode(p), null, dg)[0]?.object
        if (o) return o.value
      }
      return undefined
    }
    const one = (s: Term, p: string) => store.match(s as never, f.namedNode(p), null, dg)[0]?.object.value
    const outputs: OutputBinding[] = []
    for (const s of [island, ...classes]) {
      for (const q of store.match(f.namedNode(s), f.namedNode(EXT.output), null, dg)) {
        const on = one(q.object, EXT.on)
        const emits = one(q.object, EXT.emits)
        if (!on || !emits) continue
        const fields = store.match(q.object as never, f.namedNode(EXT.field), null, dg).flatMap((fq) => {
          const path = one(fq.object, EXT.path)
          const predicate = one(fq.object, EXT.predicate)
          return path && predicate ? [{ path, predicate }] : []
        })
        outputs.push({ on, emits, path: one(q.object, EXT.path), fields })
      }
    }
    const adapterIri = said(EXT.adapter) ?? (said(EXT.tag) ? EXT.CustomElement : EXT.Widget)
    const adapter = this.opts.adapters?.[adapterIri] ?? (adapterIri === EXT.CustomElement ? customElementAdapter : adapterIri === EXT.Widget ? widgetAdapter : undefined)
    const m: Mounted = { key, island, el, pushed: new Map(), outputs, applying: false, gone: false }
    this.mounted.set(key, m)
    if (!adapter) return this.fail(new Error(`No adapter for <${adapterIri}>`), island)
    const current = this.renderer.index.islandInputs(key)
    for (const [name, i] of current) m.pushed.set(name, { key: termKey(i.value), as: i.as })
    const ctx: IslandContext = {
      island,
      tag: this.renderer.index.nodes.get(key)?.tag ?? said(EXT.tag),
      widget: said(EXT.widget),
      inputs: new Map([...current].map(([n, i]) => [n, resolved(i)])),
      outputs: [...new Set(outputs.map((o) => o.on))],
      emit: (name, payload) => this.output(m, name, payload),
      widgets: this.opts.widgets ?? {},
    }
    this.counts.mounts++
    const module = said(EXT.module)
    if (module !== undefined) {
      const loader = this.opts.modules?.[module]
      if (!loader) return this.fail(new Error(`No module registered as "${module}"`), island)
      let ready = this.loaded.get(module)
      if (!ready) this.loaded.set(module, (ready = loader()))
      const done = ready.then(
        () => !m.gone && this.start(m, adapter, ctx),
        (e: unknown) => this.fail(e as Error, island),
      )
      this.loading.add(done)
      void done.finally(() => this.loading.delete(done))
      return
    }
    this.start(m, adapter, ctx)
  }

  /** Mount through the adapter (its module, if any, loaded). */
  private start(m: Mounted, adapter: IslandAdapter, ctx: IslandContext) {
    const island = m.island
    const el = m.el
    try {
      m.applying = true
      const made = adapter(el, ctx)
      m.applying = false
      if (made instanceof Promise) {
        const done = made.then(
          (h) => {
            if (m.gone) return h.destroy()
            m.handle = h
            this.push(m)
          },
          (e: unknown) => this.fail(e as Error, island),
        )
        this.loading.add(done)
        void done.finally(() => this.loading.delete(done))
      } else {
        m.handle = made
        // Inputs that changed while its module loaded.
        this.push(m)
      }
    } catch (e) {
      m.applying = false
      this.fail(e as Error, island)
    }
  }

  /** Push the inputs whose value changed since the last push. */
  private push(m: Mounted) {
    const current = this.renderer.index.islandInputs(m.key)
    const changed = new Map<string, Input>()
    for (const [name, i] of current) {
      const k = termKey(i.value)
      if (m.pushed.get(name)?.key === k) continue
      m.pushed.set(name, { key: k, as: i.as })
      changed.set(name, resolved(i))
    }
    // An input that is no longer stated is pushed as undefined (an attribute is removed).
    for (const [name, was] of [...m.pushed]) {
      if (current.has(name)) continue
      m.pushed.delete(name)
      changed.set(name, { name, value: undefined, as: was.as })
    }
    if (!changed.size) return
    this.counts.updates++
    m.applying = true
    try {
      m.handle!.update(changed)
    } catch (e) {
      this.fail(e as Error, m.island)
    } finally {
      m.applying = false
    }
  }

  private output(m: Mounted, name: string, payload: unknown) {
    if (m.gone) return
    // What the component says while it is being told something is its echo of it.
    if (m.applying) return void this.counts.echoes++
    const store = this.bus.runtime.store
    const f = store.factory
    for (const b of m.outputs) {
      if (b.on !== name) continue
      const v = b.path !== undefined ? at(payload, b.path) : undefined
      const about: [string, Term][] = []
      for (const field of b.fields) {
        const fv = at(payload, field.path)
        if (fv === undefined || fv === null) continue
        about.push([field.predicate, literal(f, fv)])
      }
      this.counts.outputs++
      const value = v === undefined || v === null ? undefined : typeof v === 'object' ? JSON.stringify(v) : String(v)
      const handled = this.bus.emit(b.emits, m.island, { value, about })
      if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) this.opts.onHandled?.(handled)
    }
  }

  private destroy(m: Mounted) {
    m.gone = true
    if (!m.handle) return
    this.counts.destroys++
    try {
      m.handle.destroy()
    } catch (e) {
      this.fail(e as Error, m.island)
    }
  }

  private fail(e: Error, island: string) {
    if (this.opts.onError) this.opts.onError(e, island)
    else console.error(`island <${island}>:`, e)
  }

  /** The island's classes and their superclasses, most specific first. */
  private classesOf(island: string): string[] {
    const store = this.bus.runtime.store
    const f = store.factory
    const out: string[] = []
    const todo = store.match(f.namedNode(island), f.namedNode(RDF_TYPE), null, f.defaultGraph()).map((q) => q.object.value)
    while (todo.length) {
      const c = todo.shift()!
      if (out.includes(c)) continue
      out.push(c)
      todo.push(...store.match(f.namedNode(c), f.namedNode(RDFS_SUBCLASS), null, f.defaultGraph()).map((q) => q.object.value))
    }
    return out
  }
}

function literal(f: EventBus['runtime']['store']['factory'], v: unknown): Term {
  if (typeof v === 'number') return Number.isInteger(v) ? f.literal(String(v), f.namedNode(XSD + 'integer')) : f.literal(String(v), f.namedNode(XSD + 'decimal'))
  if (typeof v === 'boolean') return f.literal(String(v), f.namedNode(XSD + 'boolean'))
  if (typeof v === 'object') return f.literal(JSON.stringify(v), f.namedNode(RDF_JSON))
  return f.literal(String(v))
}
