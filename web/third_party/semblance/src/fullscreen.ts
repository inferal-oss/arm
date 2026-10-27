// The fullscreen effect (ontology/layers/fullscreen.ttl). Two facts on a component, performed here:
// `fs:fullscreen true` is shown with the host's full screen, `fs:fullViewport true` fills the window
// (the page's top layer: a manual popover the theme sizes), and retracting them puts it back; with
// both, full screen wins. How it is shown, and that it is back in its place, come back as events
// addressed to the component (fs:Entered, fs:Exited), handled by ordinary reactions. The host's full
// screen is asked for in the same task as the change - the click's or the key press's, while the
// page still has the person's activation, which browsers require. Where the host cannot (no
// activation, a frame without allowfullscreen, a refusal, no Fullscreen API, no answer), it fills
// the window instead (fs:Maximized). Esc leaves a filled window as it leaves the host's full screen.
// Facts already in the graph when it attaches (a restored session) are retracted: the page is not
// shown so.
import type { NamedNode } from '@rdfjs/types'
import type { EventBus, Handled } from './behaviour/events.ts'
import { fullscreenPart } from './layers/fullscreen.ts'
import { FS } from './ns.ts'
import type { Delta } from './rdf.ts'

/** The host's full screen: the document's Fullscreen API, or a stand-in. */
export interface FullscreenHost {
  /** Whether it may show this element full screen at all (document.fullscreenEnabled). */
  enabled(el: Element): boolean
  /** Show it full screen (Element.requestFullscreen): rejects where the host refuses. */
  request(el: Element): Promise<void>
  /** Leave full screen (document.exitFullscreen). */
  exit(el: Element): Promise<void>
  /** What it shows full screen now (document.fullscreenElement). */
  current(el: Element): Element | null
  /** Call back whenever it enters or leaves full screen (fullscreenchange); returns how to stop. */
  watch(changed: () => void): () => void
}

/** The document's Fullscreen API. */
export function documentFullscreen(doc: Document | undefined = globalThis.document): FullscreenHost {
  return {
    enabled: (el) => !!el.ownerDocument.fullscreenEnabled && typeof el.requestFullscreen === 'function',
    request: (el) => el.requestFullscreen(),
    exit: (el) => (el.ownerDocument.fullscreenElement ? el.ownerDocument.exitFullscreen() : Promise.resolve()),
    current: (el) => el.ownerDocument.fullscreenElement,
    watch: (changed) => {
      doc?.addEventListener('fullscreenchange', changed)
      return () => doc?.removeEventListener('fullscreenchange', changed)
    },
  }
}

export interface FullscreenOptions {
  /** The element of a rendered node (by its IRI), if it is shown. */
  element: (iri: string) => Element | undefined
  /** The document whose Esc leaves the fallback (and whose full screen it is, by default). */
  document?: Document
  /** The host's full screen (the document's by default); null: there is none, so always the fallback. */
  host?: FullscreenHost | null
  /**
   * How long the host may take to show it, in ms (2000): a host that neither shows it nor refuses
   * by then (an embedding that never answers) gets the fallback.
   */
  settleMs?: number
  /** After each event a reaction handled (e.g. to patch the DOM). */
  onHandled?: (h: Handled) => void
}

type Popover = Element & { showPopover?: () => void; hidePopover?: () => void }
type How = 'pending' | 'native' | 'maximized' | 'viewport'
/** A component the effect shows: the element, and how (pending: the host was asked). */
interface Shown {
  el: Element
  how: How
}
const PRESENTATION: Record<Exclude<How, 'pending'>, string> = { native: FS.Native, maximized: FS.Maximized, viewport: FS.Viewport }

export class Fullscreen {
  private shown = new Map<string, Shown>()
  private pending = new Set<Promise<void>>()
  private detach?: () => void
  private readonly host: FullscreenHost | null

  constructor(
    private readonly bus: EventBus,
    private readonly opts: FullscreenOptions,
  ) {
    this.host = opts.host !== undefined ? opts.host : documentFullscreen(opts.document)
  }

  /** Perform the facts from now on. Returns a function that stops it. */
  attach(): () => void {
    const off = this.bus.runtime.onInput((graph, delta) => this.changed(graph, delta))
    const doc = this.opts.document ?? globalThis.document
    const unwatch = this.host?.watch(() => this.hostChanged())
    const key = (e: KeyboardEvent) => this.key(e)
    doc?.addEventListener('keydown', key)
    this.detach = () => {
      off()
      unwatch?.()
      doc?.removeEventListener('keydown', key)
      this.detach = undefined
    }
    // A restored fact: the page does not show it so, so the graph must not say it does.
    const store = this.bus.runtime.store
    const f = store.factory
    const restored = new Set<string>()
    for (const p of [FS.fullscreen, FS.fullViewport])
      for (const q of store.match(null, f.namedNode(p), null, f.defaultGraph())) if (q.object.value === 'true' && q.subject.termType === 'NamedNode') restored.add(q.subject.value)
    for (const c of restored) this.later(() => this.report(FS.Exited, c))
    return this.detach
  }

  /** Resolves once every report started so far has been made (for tests). */
  async settled(): Promise<void> {
    while (this.pending.size) await Promise.all([...this.pending])
  }

  private changed(graph: string | undefined, delta: Delta) {
    if (graph !== undefined) return
    const touched = new Set<string>()
    for (const q of [...delta.add, ...delta.remove]) if (q.predicate.value === FS.fullscreen || q.predicate.value === FS.fullViewport) touched.add(q.subject.value)
    for (const c of touched) this.sync(c)
  }

  private holds(component: string, fact: string): boolean {
    const store = this.bus.runtime.store
    const f = store.factory
    return store.match(f.namedNode(component), f.namedNode(fact), null, f.defaultGraph()).some((q) => q.object.value === 'true')
  }

  /** Show the component as its facts say now: full screen, filling the window, or in its place. */
  private sync(component: string) {
    const wants = this.holds(component, FS.fullscreen) ? 'screen' : this.holds(component, FS.fullViewport) ? 'viewport' : undefined
    const cur = this.shown.get(component)
    if (!wants) {
      if (!cur) return
      this.release(component, cur)
      return this.later(() => this.report(FS.Exited, component))
    }
    const el = cur?.el ?? this.element(component)
    // Not shown: there is nothing to show so.
    if (!el) return this.later(() => this.report(FS.Exited, component))
    if (wants === 'viewport') {
      if (cur?.how === 'viewport') return
      // The window is already filled (full screen's fallback): only what it means changes.
      if (cur?.how === 'maximized') {
        cur.how = 'viewport'
        return this.later(() => this.report(FS.Entered, component, cur))
      }
      if (cur) this.release(component, cur)
      return this.fill(component, el, 'viewport')
    }
    if (cur && cur.how !== 'viewport') return
    if (cur) this.release(component, cur)
    this.request(component, el)
  }

  /** The element a component shows: its root, or the part its class names (fs:part). */
  private element(component: string): Element | undefined {
    const store = this.bus.runtime.store
    const f = store.factory
    const term = (t: string | { value: string }) => f.namedNode(typeof t === 'string' ? t : t.value)
    const read = {
      object: (s: string | { value: string }, p: string | { value: string }) => store.match(term(s), term(p), null, f.defaultGraph())[0]?.object,
      objects: (s: string | { value: string }, p: string | { value: string }) => store.match(term(s), term(p), null, f.defaultGraph()).map((q) => q.object),
    }
    return this.opts.element(`${component}/~${fullscreenPart(read, f.namedNode(component))}`)
  }

  /** Ask the host for its full screen; where it cannot, fill the window instead. */
  private request(component: string, el: Element) {
    const host = this.host
    let requested: Promise<void> | undefined
    try {
      if (host?.enabled(el)) requested = host.request(el)
    } catch {
      requested = undefined
    }
    if (!host || !requested) return this.fill(component, el, 'maximized')
    const entry: Shown = { el, how: 'pending' }
    this.shown.set(component, entry)
    let timer: ReturnType<typeof setTimeout> | undefined
    const unanswered = new Promise<void>((_, reject) => {
      timer = setTimeout(() => reject(new Error('The host did not answer.')), this.opts.settleMs ?? 2000)
    })
    // An answer after the fallback was chosen, or after the fact went: leave the host's full screen.
    requested.then(
      () => {
        if ((this.shown.get(component) !== entry || entry.how !== 'pending') && host.current(el) === el) void host.exit(el).catch(() => undefined)
      },
      () => undefined,
    )
    this.track(
      Promise.race([requested, unanswered])
        .finally(() => clearTimeout(timer))
        .then(
          () => {
            if (this.shown.get(component) !== entry || entry.how !== 'pending') return
            entry.how = 'native'
            this.report(FS.Entered, component, entry)
          },
          () => {
            if (this.shown.get(component) !== entry || entry.how !== 'pending') return
            this.shown.delete(component)
            this.fill(component, el, 'maximized')
          },
        ),
    )
  }

  /** Fill the window: the top layer, so no ancestor's overflow or stacking clips it. */
  private fill(component: string, el: Popover, how: 'maximized' | 'viewport') {
    const entry: Shown = { el, how }
    this.shown.set(component, entry)
    if (typeof el.showPopover === 'function') {
      el.setAttribute('popover', 'manual')
      try {
        el.showPopover()
      } catch {
        // Not connected, or already open: the theme's fixed position still fills the viewport.
      }
    }
    this.later(() => this.report(FS.Entered, component, entry))
  }

  /** Put it back in its place, whatever showed it (no report: the caller says why). */
  private release(component: string, entry: Shown) {
    this.shown.delete(component)
    const el = entry.el as Popover
    if (entry.how === 'native' || entry.how === 'pending') {
      if (this.host?.current(el) === el) void this.host.exit(el).catch(() => undefined)
    } else if (el.hasAttribute('popover')) {
      try {
        el.hidePopover?.()
      } catch {
        // Already hidden.
      }
      el.removeAttribute('popover')
    }
  }

  /** The host left full screen on its own (Esc, its control, another element): the facts go. */
  private hostChanged() {
    for (const [component, entry] of [...this.shown]) {
      if (entry.how !== 'native') continue
      const now = this.host?.current(entry.el) ?? null
      if (now && (now === entry.el || entry.el.contains(now))) continue
      this.shown.delete(component)
      this.report(FS.Exited, component)
    }
  }

  /** Esc over a filled window leaves it, as it leaves the host's full screen. */
  private key(e: KeyboardEvent) {
    if (e.key !== 'Escape' || e.defaultPrevented) return
    const last = [...this.shown].filter(([, s]) => s.how === 'maximized' || s.how === 'viewport').at(-1)
    if (!last) return
    e.preventDefault()
    this.release(last[0], last[1])
    this.report(FS.Exited, last[0])
  }

  /** After the change that asked (reactions do not run inside another's transaction). */
  private later(fn: () => void) {
    this.track(Promise.resolve().then(fn))
  }

  private track(p: Promise<void>) {
    this.pending.add(p)
    void p.finally(() => this.pending.delete(p))
  }

  private report(type: string, component: string, entry?: Shown) {
    if (!this.detach) return
    // Shown otherwise since this was due (a newer change): only the latest is said.
    if (entry && (this.shown.get(component) !== entry || entry.how === 'pending')) return
    const store = this.bus.runtime.store
    const f = store.factory
    const about: [string, NamedNode | ReturnType<typeof f.literal>][] = []
    if (entry) {
      const how = f.namedNode(PRESENTATION[entry.how as Exclude<How, 'pending'>])
      about.push([FS.presentation, how])
      for (const q of store.match(how, f.namedNode(FS.says), null, f.defaultGraph())) if (q.object.termType === 'Literal') about.push([FS.says, q.object])
    }
    const handled = this.bus.emit(type, component, { about })
    if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) this.opts.onHandled?.(handled)
  }
}
