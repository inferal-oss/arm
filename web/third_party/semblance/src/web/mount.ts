import { EventBus, type Handled, type ReactionFn } from '../behaviour/events.ts'
import type { FnRule } from '../engine/context.ts'
import { DEFAULT_LAYERS, MOD } from '../ns.ts'
import { Islands, type IslandAdapter, type WidgetLike } from '../foreign.ts'
import { DomRenderer } from '../render/dom.ts'
import { type Projection, Runtime } from '../runtime.ts'
import type { GraphStore } from '../store/types.ts'
import { compileStyles } from '../style/compile.ts'
import { Files, type FilesOptions } from '../files.ts'
import { Fullscreen, type FullscreenOptions } from '../fullscreen.ts'
import { attachConnectors } from './connectors.ts'
import { attachSnaps } from './snap.ts'
import { attachLightboxes } from './lightbox.ts'
import { hasStore, initWeb, loadStore, type StoreKind } from './load.ts'

export { DEFAULT_LAYERS }

export interface MountOptions {
  /** The IRI of the component to show: the root of the projection. */
  root: string
  /** Turtle to load besides Semblance's own ontology: your app's components, your component classes and their rules. */
  graphs?: string[]
  /** The store; Semblance's own WASM engine when this build has it, else Hybrid. */
  store?: StoreKind
  /** Which implementation of a rendering to use when it has both: SPARQL (the default) or a function. */
  prefer?: 'sparql' | 'function'
  /** Function rules, by the name a sem:Rule's sem:function gives. */
  functions?: Record<string, FnRule>
  /** Reaction functions, by the name a beh:Reaction's sem:function gives (for stores without SPARQL). */
  reactions?: Record<string, ReactionFn>
  /** Layer IRIs to enable (DEFAULT_LAYERS otherwise). */
  layers?: Iterable<string>
  /** The viewer's languages, most preferred first (navigator.languages otherwise). */
  languages?: string[]
  /** Called after each event is handled and the DOM patched. */
  onHandled?: (h: Handled) => void
  /** The files effect (c:FileDrop: the picker, drops, uploads and their handlers); false leaves it off. */
  files?: Omit<FilesOptions, 'onHandled'> | false
  /** The fullscreen effect (fs:fullscreen, fs:fullViewport): its host, or how long it may take; false leaves it off. */
  fullscreen?: Omit<FullscreenOptions, 'element' | 'onHandled'> | false
  /** Foreign components (ext:Island, docs/FOREIGN.md): widgets by their ext:widget name. */
  widgets?: Record<string, WidgetLike>
  /** Loaders by the name ext:module gives (a dynamic import: a custom element's definition, a library). */
  modules?: Record<string, () => Promise<unknown>>
  /** Adapters for kinds of foreign component beyond the built-in ones, by their ext:Adapter IRI. */
  adapters?: Record<string, IslandAdapter>
}

export interface MountedApp {
  store: GraphStore
  runtime: Runtime
  projection: Projection
  renderer: DomRenderer
  bus: EventBus
  /** The files effect, unless it was left off: the contents of picked and dropped files (fileOf). */
  files?: Files
  /** The islands effect: foreign components mounted in the slots islands render. */
  islands: Islands
  /** Patch the DOM with whatever changed since (after changing the store through runtime.apply). */
  flush(): void
  /** Detach events and idle work, and remove the styles. The DOM is left as it is. */
  unmount(): void
}

/**
 * Mount a component graph into an element: load the store, compile the theme, project the
 * root on screen, patch it into `element` and route the element's events to reactions.
 * Hidden parts are evaluated in idle time; connectors (c:Connector) are drawn where their ends
 * are; picked, dropped and uploaded files go through the files effect (opts.files), and what is
 * asserted full screen or filling the window through the fullscreen effect (opts.fullscreen); foreign components (ext:Island) are mounted in their slots. Persistence, navigation and the other host effects are opt-in: attach them to `runtime` (see docs/GETTING-STARTED.md).
 */
export async function mount(element: HTMLElement, opts: MountOptions): Promise<MountedApp> {
  const kind = opts.store ?? (hasStore('wat') ? 'wat' : 'hybrid')
  // A store without SPARQL (N3) can only run function rules.
  const prefer = kind === 'n3' ? 'function' : (opts.prefer ?? 'sparql')
  await initWeb(kind)
  const store = loadStore(kind, ...(opts.graphs ?? []))
  const runtime = new Runtime(store)
  const projection = runtime.project({
    modality: MOD.Screen,
    layers: opts.layers ?? DEFAULT_LAYERS,
    prefer,
    functions: opts.functions,
    languages: opts.languages ?? [...(globalThis.navigator?.languages ?? [])],
    name: 'screen',
    roots: [opts.root],
  })

  const doc = element.ownerDocument
  const sheet = new (doc.defaultView ?? globalThis).CSSStyleSheet()
  sheet.replaceSync(compileStyles(store))
  doc.adoptedStyleSheets = [...doc.adoptedStyleSheets, sheet]

  const renderer = new DomRenderer(projection.index, element, opts.root)
  const flush = () => renderer.flush()

  // Hidden parts (other tabs, closed panels) are evaluated a slice at a time when the page is idle.
  const idle = (cb: (d: { timeRemaining(): number }) => void) =>
    'requestIdleCallback' in globalThis ? requestIdleCallback(cb, { timeout: 1000 }) : setTimeout(() => cb({ timeRemaining: () => 8 }), 30)
  let warming = false
  let stopped = false
  const warm = (deadline: { timeRemaining(): number }) => {
    if (stopped) return
    const more = runtime.warm(Math.max(2, deadline.timeRemaining() - 2))
    flush()
    warming = more
    if (more) idle(warm)
  }
  const scheduleWarmup = () => {
    if (!warming && !stopped) (warming = true), idle(warm)
  }

  const bus = new EventBus(runtime, projection, { functions: opts.reactions, prefer })
  const detach = bus.attach(renderer, (h) => {
    flush()
    scheduleWarmup()
    opts.onHandled?.(h)
  })
  flush()
  scheduleWarmup()
  // Connectors are drawn where their ends are (nothing to do while there is none).
  const connectors = attachConnectors(element)
  // Files: a file:Pick opens the system picker, files dropped on a file drop's zone arrive, and
  // accepted files are uploaded as their drop says; each reports back as events on the bus.
  const files =
    opts.files === false
      ? undefined
      : new Files(bus, {
          ...opts.files,
          onHandled: (h) => {
            flush()
            scheduleWarmup()
            opts.onHandled?.(h)
          },
        })
  const detachFiles = files?.attach(renderer)
  // Full screen and filling the window: fs:fullscreen and fs:fullViewport on a component are shown
  // so; the browser leaving on its own, or Esc, comes back as an event.
  const detachFullscreen =
    opts.fullscreen === false
      ? undefined
      : new Fullscreen(bus, {
          ...opts.fullscreen,
          element: (iri) => {
            const n = renderer.nodeOf(iri)
            return n?.nodeType === 1 ? (n as Element) : undefined
          },
          document: opts.fullscreen?.document ?? doc,
          onHandled: (h) => {
            flush()
            scheduleWarmup()
            opts.onHandled?.(h)
          },
        }).attach()
  // Swiped tracks (a carousel, a lightbox) report where they came to rest and follow the graph.
  const snaps = attachSnaps(element)
  // Lightboxes grow from their image and give it back, slide between images, zoom and pan.
  const lightboxes = attachLightboxes(element)
  // Foreign components are mounted in their islands' slots after each patch (nothing to do while there is none).
  const islands = new Islands(bus, renderer, {
    widgets: opts.widgets,
    modules: opts.modules,
    adapters: opts.adapters,
    onHandled: (h) => {
      flush()
      scheduleWarmup()
      opts.onHandled?.(h)
    },
  })
  const detachIslands = islands.attach()

  return {
    store,
    runtime,
    projection,
    renderer,
    bus,
    files,
    islands,
    flush,
    unmount() {
      stopped = true
      detach()
      detachFiles?.()
      detachFullscreen?.()
      detachIslands()
      connectors.dispose()
      snaps.dispose()
      lightboxes.dispose()
      doc.adoptedStyleSheets = doc.adoptedStyleSheets.filter((s) => s !== sheet)
    },
  }
}
