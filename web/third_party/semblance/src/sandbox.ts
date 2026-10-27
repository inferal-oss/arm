// The host effect of live examples (ontology/components/live.ttl). Each c:LiveExample's sources run in
// a sandbox of their own: a store with Semblance's ontology and the sources, its own runtime, its own
// projection, mounted in the example's host-owned slot with its own event bus. So a source that does
// not parse, a rule that throws or a reaction that fails stays inside the sandbox, and the site never
// sees the example's graph. What happened is written back to the site's graph, as data the example's
// rendering shows: c:sandboxState, c:problem, c:renderTime, c:edited. A sandbox is built when its
// example is first shown, and rebuilt when its sources change (after typing pauses); a hidden example
// costs nothing. With c:dataPreview, the example's data (that CONSTRUCT over its graph) is written
// back too, as lines of Turtle (c:dataLine), after the build and after every interaction that
// changed it, with what changed marked (c:lineChange, c:changedLine, c:triplesAdded/Removed).
import { Parser } from 'n3'
import type { Quad, Term } from '@rdfjs/types'
import { EventBus } from './behaviour/events.ts'
import { Fullscreen, type FullscreenHost } from './fullscreen.ts'
import type { FnRule } from './engine/context.ts'
import { DEFAULT_LAYERS, MOD, NS, RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE, SEM, STY, XSD_BOOLEAN, XSD_INTEGER } from './ns.ts'
import { compactIri } from './prefixes.ts'
import { type Delta, mint, quadKey } from './rdf.ts'
import { DomRenderer } from './render/dom.ts'
import { type Projection, Runtime } from './runtime.ts'
import type { GraphStore } from './store/types.ts'
import { compileStyles } from './style/compile.ts'

const c = NS.c
export const LIVE = {
  LiveExample: c + 'LiveExample',
  sources: c + 'sources',
  exampleRoot: c + 'exampleRoot',
  textPreview: c + 'textPreview',
  dataPreview: c + 'dataPreview',
  dataLine: c + 'dataLine',
  DataLine: c + 'DataLine',
  lineSubject: c + 'lineSubject',
  lineNode: c + 'lineNode',
  linePredicate: c + 'linePredicate',
  lineObject: c + 'lineObject',
  subjectToken: c + 'subjectToken',
  predicateToken: c + 'predicateToken',
  objectToken: c + 'objectToken',
  lineKey: c + 'lineKey',
  lineChange: c + 'lineChange',
  LineAdded: c + 'LineAdded',
  LineRemoved: c + 'LineRemoved',
  changedLine: c + 'changedLine',
  triplesAdded: c + 'triplesAdded',
  triplesRemoved: c + 'triplesRemoved',
  original: c + 'original',
  value: c + 'value',
  label: c + 'label',
  sandboxState: c + 'sandboxState',
  Live: c + 'SandboxLive',
  Stale: c + 'SandboxStale',
  Broken: c + 'SandboxBroken',
  problem: c + 'problem',
  SandboxProblem: c + 'SandboxProblem',
  message: c + 'message',
  inSource: c + 'inSource',
  sourceLine: c + 'sourceLine',
  renderTime: c + 'renderTime',
  edited: c + 'edited',
  interacted: c + 'interacted',
  copied: c + 'copied',
} as const

/**
 * Turtle declarations of every prefix Semblance knows, on one line: a source may use them without
 * declaring them (its own lines then start at line 2, and are reported as from 1).
 */
const PREAMBLE = Object.entries(NS).map(([p, ns]) => `@prefix ${p}: <${ns}> .`).join(' ') + '\n'

/** One source of an example: its editor in the site's graph, its name and its text. */
export interface SandboxSource {
  editor?: string
  name: string
  text: string
}

/** A problem a build met; an error stops the build, a warning does not. */
export interface SandboxProblem {
  message: string
  severity: 'error' | 'warning'
  /** The editor of the source it is in. */
  source?: string
  /** The line of that source, from 1. */
  line?: number
}

export interface SandboxOptions {
  /** A store with Semblance's ontology and the given Turtle loaded (loadStore of src/web or src/node, with a store kind). */
  load: (...turtle: string[]) => GraphStore
  layers?: Iterable<string>
  prefer?: 'sparql' | 'function'
  /** Function rules beyond the built-in ones, by name. */
  functions?: Record<string, FnRule>
  /**
   * How long one turn of a sandbox may run, in ms (1500). Over it, the sandbox stops itself (its
   * watchdog's held work is dropped, src/engine/watchdog.ts) and says so inline, rather than
   * freezing the site: a build is reported broken, an interaction leaves a problem.
   */
  budgetMs?: number
}

/** What a stalled sandbox says: the budget, and the slowest work of the turn. */
function stallProblem(runtime: Runtime, sources: SandboxSource[], budgetMs: number): SandboxProblem {
  const s = runtime.watchdog.stall
  const w = s?.culprit
  const what = w ? ` The slowest work was ${short(w.iri, sources)}${w.focus ? ` for ${short(w.focus.split(' ')[0]!, sources)}` : ''} (${Math.round(w.ms)} ms).` : ''
  return { message: `Stopped: rendering took longer than ${budgetMs} ms, and ${s?.pending ?? runtime.held} renderings were left undone.${what}`, severity: 'error' }
}

/** A built sandbox: the example's own store, runtime and projections. */
export interface Sandbox {
  root: string
  store: GraphStore
  runtime: Runtime
  screen: Projection
  text?: Projection
  /** Whether its sources declare styles of their own (sty:StyleRule, sty:Token, sty:Keyframes). */
  styled: boolean
}

export interface Build {
  sandbox?: Sandbox
  problems: SandboxProblem[]
  /** Whole milliseconds the build took. */
  ms: number
}

const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now())

/** "Expected entity but got . on line 4." -> the message and the source's own line. */
function parseError(e: Error, source: SandboxSource): SandboxProblem {
  const line = (e as Error & { context?: { line?: number } }).context?.line
  const message = e.message.replace(/\s+on line \d+\.?$/, '').replace(/^./, (ch) => ch.toUpperCase())
  return { message, severity: 'error', source: source.editor, line: line !== undefined ? Math.max(1, line - 1) : undefined }
}

const STYLE_CLASSES = new Set([STY.StyleRule, NS.sty + 'Token', NS.sty + 'Keyframes'])

/**
 * Build a sandbox for `sources` (in load order) showing `root`: parse each source (an error names
 * its line), load them over Semblance's ontology, project `root` on screen (and in the Text
 * modality with `withText`), and check that it rendered. Shape violations are warnings.
 */
export function buildSandbox(sources: SandboxSource[], root: string, opts: SandboxOptions & { languages?: string[]; withText?: boolean }): Build {
  const t0 = now()
  const problems: SandboxProblem[] = []
  const done = (sandbox?: Sandbox): Build => ({ sandbox, problems, ms: Math.max(0, Math.round(now() - t0)) })
  let styled = false
  for (const s of sources) {
    try {
      const quads = new Parser({ format: 'text/turtle' }).parse(PREAMBLE + s.text)
      styled ||= quads.some((q) => q.predicate.value === RDF_TYPE && STYLE_CLASSES.has(q.object.value))
    } catch (e) {
      problems.push(parseError(e as Error, s))
    }
  }
  if (problems.length) return done()
  let store: GraphStore
  try {
    store = opts.load(...sources.map((s) => PREAMBLE + s.text))
  } catch (e) {
    problems.push({ message: (e as Error).message, severity: 'error' })
    return done()
  }
  // A build that fails lets go of its store at once: Oxigraph's share one WebAssembly memory.
  let runtime: Runtime | undefined
  const failed = () => (runtime ? runtime.dispose() : store.dispose?.(), done())
  const f = store.factory
  if (!store.match(f.namedNode(root), null, null).length) {
    problems.push({ message: `Nothing to show: ${short(root, sources)} is not described in the source.`, severity: 'error' })
    return failed()
  }
  const warnings: string[] = []
  try {
    runtime = new Runtime(store)
    const budgetMs = opts.budgetMs ?? 1500
    runtime.watchdog.budgetMs = budgetMs
    const base = { layers: opts.layers ?? DEFAULT_LAYERS, prefer: opts.prefer, functions: opts.functions, languages: opts.languages, roots: [root] }
    const screen = runtime.project({ ...base, modality: MOD.Screen, name: 'screen' }, (e) => warnings.push(e.message))
    const text = opts.withText ? runtime.project({ ...base, modality: MOD.Text, name: 'text' }, (e) => warnings.push(e.message)) : undefined
    // Over budget: the sandbox stops itself, and the build is broken (the site never waits on it).
    if (runtime.held) {
      problems.push(stallProblem(runtime, sources, budgetMs))
      runtime.stopStall()
      return failed()
    }
    if (!screen.index.resolve(root)) problems.push({ message: `${short(root, sources)} renders nothing: is it an instance of a component class with a rendering?`, severity: 'error' })
    for (const w of new Set(warnings)) problems.push({ message: w, severity: 'warning' })
    problems.push(...violations(store, sources))
    if (problems.some((p) => p.severity === 'error')) return failed()
    return done({ root, store, runtime, screen, text, styled })
  } catch (e) {
    problems.push({ message: (e as Error).message, severity: 'error' })
    return failed()
  }
}

/** The shapes layer's results, as warnings: "ex:cups: A tally needs a label". */
function violations(store: GraphStore, sources: SandboxSource[]): SandboxProblem[] {
  const f = store.factory
  const out: SandboxProblem[] = []
  for (const q of store.match(null, f.namedNode(NS.sh + 'resultMessage'), null)) {
    const focus = store.match(q.subject, f.namedNode(NS.sh + 'focusNode'), null)[0]?.object
    out.push({ message: `${focus ? short(focus.value, sources) + ': ' : ''}${q.object.value}`, severity: 'warning' })
  }
  return out.sort((a, b) => (a.message < b.message ? -1 : a.message > b.message ? 1 : 0))
}

/** An IRI as the sources would write it (their own prefixes, then Semblance's). */
function short(iri: string, sources: SandboxSource[]): string {
  return compactIri(iri, { ...NS, ...declared(sources.map((s) => s.text).join('\n')) })
}

function declared(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const m of text.matchAll(/@prefix\s+([A-Za-z][\w-]*)?:\s*<([^>]*)>/g)) out[m[1] ?? ''] = m[2]!
  return out
}

/**
 * A source as a complete Turtle file: the declarations of the Semblance prefixes it uses but does
 * not declare, then the source (what Copy puts on the clipboard).
 */
export function completeSource(text: string): string {
  const own = declared(text)
  // Prefixed names outside IRIs and comments.
  const bare = text.replace(/<[^<>\s]*>/g, '').replace(/(^|\s)#[^\n]*/g, '$1')
  const used = new Set([...bare.matchAll(/(?<![\w:@/.-])([A-Za-z][\w-]*):(?=[A-Za-z_\s(\[;,.]|$)/gm)].map((m) => m[1]!))
  const missing = Object.entries(NS).filter(([p]) => used.has(p) && !(p in own))
  return missing.length ? missing.map(([p, ns]) => `@prefix ${p}: <${ns}> .`).join('\n') + '\n\n' + text : text
}

// --- the host effect -------------------------------------------------------------------------------

export interface SandboxesOptions extends SandboxOptions {
  /** The site's renderer: the examples' slots are its elements. */
  renderer: DomRenderer
  /** Whether a component of the site is shown (e.g. screen.index.isShown(c, roots)). */
  shown: (component: string) => boolean
  /** Patch the site's DOM, after the sandboxes wrote their state to its graph. */
  flush: () => void
  /** The site's languages, most preferred first: a sandbox follows them. */
  languages?: () => string[]
  /** The site's stylesheet (compileStyles of its store): a sandbox's own sheet has only the rules it adds. */
  siteCss?: string
  /** How long typing pauses before a sandbox is rebuilt, in ms (300). */
  delayMs?: number
  /** Put text on the clipboard (navigator.clipboard by default). */
  copy?: (text: string) => Promise<void> | void
  /** How long a copy request stays (the button says "Copied"), in ms (1500). */
  copiedMs?: number
  /**
   * The full screen an example's own fs:fullscreen may take (none by default: a sandbox does not
   * take the person's screen; what it asserts is shown over the page instead, and Esc leaves it).
   */
  fullscreen?: FullscreenHost | null
  /**
   * How many sandboxes stay built (8). Each holds a store with the whole ontology (about 12 MB of
   * engine memory); past this, those hidden longest are let go, and built again when shown.
   */
  maxMounted?: number
}

interface Mounted {
  iri: string
  /** The site's slot element the sandbox is mounted in. */
  slot: Element
  sandbox?: Sandbox
  renderer?: DomRenderer
  textRenderer?: DomRenderer
  detach?: () => void
  sheet?: CSSStyleSheet
  timer?: ReturnType<typeof setTimeout>
  /** The texts of the build that is mounted, or of the last build tried. */
  key?: string
  languages: string
  builds: number
  /** When it was last seen shown (a count of syncs). */
  shownAt: number
  /** The data the example shows (c:dataPreview) as last found: its triples by key. */
  data?: Map<string, Quad>
}

let sandboxes = 0

let created: ((s: Sandboxes) => void) | undefined
/**
 * Hear about every Sandboxes made (one listener; undefined stops): test/setup.ts disposes each
 * test's before releasing its stores, so no rebuild waiting for typing to pause outlives them.
 */
export function watchSandboxes(listener: ((s: Sandboxes) => void) | undefined): void {
  created = listener
}

/**
 * Mounts a sandbox in every c:LiveExample that is shown, and keeps it in step with its sources.
 * Call `sync()` after each flush of the site's renderer (it is cheap when nothing was shown or
 * hidden); `attach()` follows edits.
 */
export class Sandboxes {
  private readonly mounted = new Map<string, Mounted>()
  private readonly editorOf = new Map<string, string>()
  private seenVisibility = -1
  private syncs = 0
  private syncing = false
  private detachInput?: () => void
  /** Timers that are not a sandbox's own (withdrawing a copy request). */
  private readonly timers = new Set<ReturnType<typeof setTimeout>>()
  private disposed = false
  /** While the host writes what happened to the site's graph (its own removals ask for nothing). */
  private writing = false
  /** Builds done, for tests and cost checks. */
  builds = 0

  constructor(
    readonly runtime: Runtime,
    readonly screen: Projection,
    private readonly opts: SandboxesOptions,
  ) {
    created?.(this)
  }

  /** Follow the site's input: an edit of a source rebuilds its sandbox (once typing pauses); a copy request copies. */
  attach(): () => void {
    this.detachInput = this.runtime.onInput((graph, delta) => graph === undefined && this.changed(delta))
    return () => this.dispose()
  }

  /** How many sandboxes are built now. */
  get size(): number {
    return this.mounted.size
  }

  /** The sandbox of a live example, if it is mounted. */
  sandboxOf(example: string): Sandbox | undefined {
    return this.mounted.get(example)?.sandbox
  }

  /** The renderer of a mounted sandbox (its DOM), if any. */
  rendererOf(example: string): DomRenderer | undefined {
    return this.mounted.get(example)?.renderer
  }

  /** Mount the sandboxes of the examples shown since the last call, and follow the site's language. */
  sync(): void {
    if (this.syncing || this.disposed) return
    this.syncing = true
    try {
      const languages = this.opts.languages?.() ?? []
      const lang = languages.join(',')
      for (const m of this.mounted.values()) {
        if (m.languages === lang || !m.sandbox) continue
        m.languages = lang
        for (const p of [m.sandbox.screen, m.sandbox.text]) p?.engine.reconfigure({ languages })
        m.renderer?.flush()
        m.textRenderer?.flush()
      }
      const visibility = this.screen.index.visibility
      if (visibility === this.seenVisibility) return
      this.seenVisibility = visibility
      const tick = ++this.syncs
      const store = this.runtime.store
      const f = store.factory
      for (const q of store.match(null, f.namedNode(RDF_TYPE), f.namedNode(LIVE.LiveExample), f.defaultGraph())) {
        const iri = q.subject.value
        const slot = this.opts.renderer.nodeOf(mint(iri, 'screen')) as Element | undefined
        if (!slot || !this.opts.shown(iri)) continue
        const m = this.mounted.get(iri)
        if (m?.slot === slot) {
          m.shownAt = tick
          continue
        }
        if (m) this.unmount(m)
        const fresh: Mounted = { iri, slot, languages: lang, builds: 0, shownAt: tick }
        this.mounted.set(iri, fresh)
        this.build(fresh)
      }
      // Too many built: let go of those hidden longest (shown ones stay, however many).
      const max = this.opts.maxMounted ?? 8
      if (this.mounted.size > max) {
        const hidden = [...this.mounted.values()].filter((x) => x.shownAt !== tick).sort((a, b) => a.shownAt - b.shownAt)
        for (const x of hidden.slice(0, this.mounted.size - max)) this.unmount(x)
      }
    } finally {
      this.syncing = false
    }
  }

  /** Rebuild now the sandboxes waiting for typing to pause (tests; a host about to unload). */
  settle(): void {
    for (const m of this.mounted.values()) {
      if (m.timer === undefined) continue
      clearTimeout(m.timer)
      m.timer = undefined
      this.build(m)
    }
  }

  /**
   * Stop: every sandbox's timers are cancelled, its bus and listeners detached and its runtime and
   * store released; a callback that was already on its way does nothing.
   */
  dispose(): void {
    if (this.disposed) return
    this.disposed = true
    this.detachInput?.()
    for (const t of this.timers) clearTimeout(t)
    this.timers.clear()
    for (const m of this.mounted.values()) this.unmount(m)
    this.mounted.clear()
    this.editorOf.clear()
  }

  private changed(delta: Delta) {
    for (const q of delta.remove) {
      this.touched(q)
      // Reset withdrew c:interacted: build the example again as written (at once, not after typing pauses).
      if (q.predicate.value === LIVE.interacted && !this.writing) this.rebuild(q.subject.value, 0)
    }
    for (const q of delta.add) {
      this.touched(q)
      if (q.predicate.value === LIVE.copied) void this.copy(q.subject.value, q.object.value)
    }
  }

  private touched(q: Quad) {
    const p = q.predicate.value
    if (p !== LIVE.value && p !== LIVE.original) return
    const example = this.editorOf.get(q.subject.value)
    if (example) this.rebuild(example, this.opts.delayMs ?? 300)
  }

  /** Build an example again after `ms` (a later request replaces a waiting one); with 0, even if its sources are the same. */
  private rebuild(example: string, ms: number) {
    const m = this.mounted.get(example)
    if (!m) return
    if (m.timer !== undefined) clearTimeout(m.timer)
    if (ms === 0) m.key = undefined
    m.timer = setTimeout(() => {
      m.timer = undefined
      if (!this.disposed && this.mounted.get(m.iri) === m) this.build(m)
    }, ms)
  }

  /** The example's sources from the site's graph: each editor's text as edited, else as written. */
  private sources(example: string): SandboxSource[] {
    const store = this.runtime.store
    const f = store.factory
    const dg = f.defaultGraph()
    const one = (s: Term, p: string) => store.match(s, f.namedNode(p), null, dg)[0]?.object
    const out: SandboxSource[] = []
    for (let cell = one(f.namedNode(example), LIVE.sources); cell && cell.value !== RDF_NIL && out.length < 64; cell = one(cell, RDF_REST)) {
      const editor = one(cell, RDF_FIRST)
      if (!editor) continue
      this.editorOf.set(editor.value, example)
      const text = one(editor, LIVE.value)?.value ?? one(editor, LIVE.original)?.value ?? ''
      out.push({ editor: editor.value, name: one(editor, LIVE.label)?.value ?? editor.value.replace(/^.*[#/]/, ''), text })
    }
    return out
  }

  private build(m: Mounted) {
    const store = this.runtime.store
    const f = store.factory
    const example = f.namedNode(m.iri)
    const sources = this.sources(m.iri)
    const key = JSON.stringify(sources.map((s) => s.text))
    const root = store.match(example, f.namedNode(LIVE.exampleRoot), null, f.defaultGraph())[0]?.object.value
    const withText = store.match(example, f.namedNode(LIVE.textPreview), null, f.defaultGraph()).some((q) => q.object.value === 'true')
    const edited = sources.some((s) => {
      const editor = f.namedNode(s.editor!)
      const original = store.match(editor, f.namedNode(LIVE.original), null)[0]?.object.value
      const value = store.match(editor, f.namedNode(LIVE.value), null)[0]?.object.value
      return value !== undefined && value !== original
    })
    if (key === m.key && m.sandbox) {
      // Typed and typed back: the sandbox shows these sources already.
      this.write(m.iri, { edited })
      return
    }
    m.key = key
    this.builds++
    m.builds++
    const t0 = now()
    // A build is a turn of the site's watchdog (no budget of its own there): a hang it causes is blamed on the example.
    const wd = this.runtime.watchdog
    const built = wd.turn(() => {
      const b = root
        ? buildSandbox(sources, root, { ...this.opts, languages: this.opts.languages?.(), withText })
        : { problems: [{ message: 'The example names nothing to show (c:exampleRoot).', severity: 'error' as const }], ms: 0 }
      wd.note('rule', LIVE.LiveExample, m.iri, 1, now() - t0)
      return b
    })
    if (built.sandbox) this.mount(m, built.sandbox)
    const ms = Math.max(0, Math.round(now() - t0))
    const state = built.sandbox ? LIVE.Live : m.sandbox ? LIVE.Stale : LIVE.Broken
    this.write(m.iri, { state, problems: built.problems, ms: built.sandbox ? ms : undefined, edited, interacted: built.sandbox ? false : undefined })
  }

  /** Put a built sandbox in the slot, in place of the one there (whose bus and styles go with it). */
  private mount(m: Mounted, sandbox: Sandbox) {
    const doc = m.slot.ownerDocument
    const n = ++sandboxes
    const host = doc.createElement('div')
    host.setAttribute('data-sem-sandbox', String(n))
    const renderer = new DomRenderer(sandbox.screen.index, host, sandbox.root)
    let textRenderer: DomRenderer | undefined
    const textSlot = sandbox.text && (this.opts.renderer.nodeOf(mint(m.iri, 'text')) as Element | undefined)
    let textHost: Element | undefined
    if (sandbox.text && textSlot) {
      textHost = doc.createElement('div')
      textRenderer = new DomRenderer(sandbox.text.index, textHost, sandbox.root)
    }
    const flush = () => {
      renderer.flush()
      textRenderer?.flush()
    }
    flush()
    const bus = new EventBus(sandbox.runtime, sandbox.screen, { prefer: this.opts.prefer })
    const failed = (e: Error) => this.write(m.iri, { problems: [{ message: e.message, severity: 'error' }] })
    const detachBus = bus.attach(
      renderer,
      (h) => {
        flush()
        if (!h.delta.add.length && !h.delta.remove.length) return
        this.followData(m, sandbox, true)
        this.write(m.iri, { interacted: true })
      },
      failed,
    )
    // What the example asserts full screen is shown over the page (or full screen, if allowed).
    const detachFullscreen = new Fullscreen(bus, {
      element: (iri) => {
        const n = renderer.nodeOf(iri)
        return n?.nodeType === 1 ? (n as Element) : undefined
      },
      document: doc,
      host: this.opts.fullscreen ?? null,
      onHandled: () => flush(),
    }).attach()
    // An interaction over budget: the sandbox stops itself (what it left undone keeps its last
    // rendering) and the problem shows inline.
    const detachStall = sandbox.runtime.watchdog.onStall(() =>
      queueMicrotask(() => {
        if (this.disposed || m.sandbox !== sandbox || !sandbox.runtime.held) return
        const problem = stallProblem(sandbox.runtime, this.sources(m.iri), this.opts.budgetMs ?? 1500)
        sandbox.runtime.stopStall()
        flush()
        this.write(m.iri, { problems: [problem] })
      }),
    )
    const detachRefresh = sandbox.runtime.onRefresh(flush)
    const detachTimer = sandbox.runtime.onTimer(flush)
    const sheet = sandbox.styled ? this.sheet(doc, sandbox.store, n) : undefined
    // The new rendering replaces the old in one step.
    this.unmountSandbox(m)
    m.slot.replaceChildren(host)
    if (textSlot && textHost) textSlot.replaceChildren(textHost)
    m.sandbox = sandbox
    m.renderer = renderer
    m.textRenderer = textRenderer
    m.sheet = sheet
    this.followData(m, sandbox, false)
    m.detach = () => {
      detachStall()
      detachBus()
      detachFullscreen()
      detachRefresh()
      detachTimer()
    }
  }

  /** The style rules the sandbox adds to the site's, scoped to its own element. */
  private sheet(doc: Document, store: GraphStore, n: number): CSSStyleSheet | undefined {
    const Sheet = (doc.defaultView as (Window & typeof globalThis) | null)?.CSSStyleSheet
    if (!Sheet || !('adoptedStyleSheets' in doc)) return undefined
    const css = scopedCss(compileStyles(store), this.opts.siteCss ?? '', `[data-sem-sandbox="${n}"]`)
    if (!css) return undefined
    const sheet = new Sheet()
    sheet.replaceSync(css)
    doc.adoptedStyleSheets = [...doc.adoptedStyleSheets, sheet]
    return sheet
  }

  private unmountSandbox(m: Mounted) {
    m.detach?.()
    // The sandbox's runtime and store are its own: release them (Oxigraph's share one WebAssembly memory).
    m.sandbox?.runtime.dispose()
    if (m.sheet) m.slot.ownerDocument.adoptedStyleSheets = m.slot.ownerDocument.adoptedStyleSheets.filter((s) => s !== m.sheet)
    m.detach = m.sheet = m.sandbox = m.renderer = m.textRenderer = undefined
  }

  private unmount(m: Mounted) {
    if (m.timer !== undefined) clearTimeout(m.timer)
    this.unmountSandbox(m)
    this.mounted.delete(m.iri)
  }

  /** Write what a build found to the site's graph (only what changed; what is not given stays), and patch the site. */
  private write(example: string, status: { state?: string; problems?: SandboxProblem[]; ms?: number; edited?: boolean; interacted?: boolean }) {
    const store = this.runtime.store
    const f = store.factory
    const dg = f.defaultGraph()
    const s = f.namedNode(example)
    const nn = (iri: string) => f.namedNode(iri)
    const integer = (n: number) => f.literal(String(n), nn(XSD_INTEGER))
    const current: Quad[] = []
    const given = [
      [LIVE.sandboxState, status.state],
      [LIVE.problem, status.problems],
      [LIVE.renderTime, status.ms],
      [LIVE.edited, status.edited],
      [LIVE.interacted, status.interacted],
    ] as const
    for (const [p, v] of given) {
      if (v === undefined) continue
      for (const q of store.match(s, nn(p), null, dg)) {
        current.push(q)
        if (p === LIVE.problem) current.push(...store.match(q.object, null, null, dg))
      }
    }
    const desired: Quad[] = []
    if (status.state) desired.push(f.quad(s, nn(LIVE.sandboxState), nn(status.state)))
    if (status.ms !== undefined) desired.push(f.quad(s, nn(LIVE.renderTime), integer(status.ms)))
    if (status.edited) desired.push(f.quad(s, nn(LIVE.edited), f.literal('true', nn(XSD_BOOLEAN))))
    if (status.interacted) desired.push(f.quad(s, nn(LIVE.interacted), f.literal('true', nn(XSD_BOOLEAN))))
    ;(status.problems ?? []).slice(0, 20).forEach((p, i) => {
      const node = nn(`${example}/problem-${i}`)
      desired.push(
        f.quad(s, nn(LIVE.problem), node),
        f.quad(node, nn(RDF_TYPE), nn(LIVE.SandboxProblem)),
        f.quad(node, nn(SEM.order), integer(i)),
        f.quad(node, nn(LIVE.message), f.literal(p.message)),
        f.quad(node, nn(STY.tone), nn(NS.sty + (p.severity === 'error' ? 'Danger' : 'Warning'))),
      )
      if (p.source) desired.push(f.quad(node, nn(LIVE.inSource), nn(p.source)))
      if (p.line !== undefined) desired.push(f.quad(node, nn(LIVE.sourceLine), integer(p.line)))
    })
    const keys = new Set(desired.map(quadKey))
    const had = new Set(current.map(quadKey))
    const delta: Delta = { remove: current.filter((q) => !keys.has(quadKey(q))), add: desired.filter((q) => !had.has(quadKey(q))) }
    if (!delta.add.length && !delta.remove.length) return
    this.writing = true
    try {
      this.runtime.apply(delta)
    } finally {
      this.writing = false
    }
    this.opts.flush()
  }

  /**
   * The example's data (c:dataPreview) as it is now in its sandbox, written to the site's graph as
   * lines of Turtle: after a build all of it as it is; after an interaction (`changed`) with the
   * triples it added and removed marked, and nothing written when it changed none of them.
   */
  private followData(m: Mounted, sandbox: Sandbox, changed: boolean) {
    const site = this.runtime.store
    const sf = site.factory
    const query = site.match(sf.namedNode(m.iri), sf.namedNode(LIVE.dataPreview), null, sf.defaultGraph())[0]?.object.value
    if (!query) return
    const sources = this.sources(m.iri)
    const prefixes = { ...NS, ...declared(sources.map((s) => s.text).join('\n')) }
    const head = Object.entries(prefixes).map(([p, ns]) => `PREFIX ${p}: <${ns}>`).join('\n')
    let quads: Quad[]
    try {
      quads = sandbox.store.construct(head + '\n' + query, [sandbox.store.factory.defaultGraph()])
    } catch (e) {
      console.warn('[semblance] the data of a live example could not be read', e)
      return
    }
    const now = new Map(quads.map((q) => [quadKey(q), q]))
    const before = m.data
    m.data = now
    const added = changed && before ? [...now.keys()].filter((k) => !before.has(k)) : []
    const removed = changed && before ? [...before.keys()].filter((k) => !now.has(k)) : []
    if (changed && before && !added.length && !removed.length) return
    const lines = dataLines(now, removed.map((k) => before!.get(k)!), new Set(added), prefixes)
    const s = sf.namedNode(m.iri)
    const nn = (iri: string) => sf.namedNode(iri)
    const integer = (n: number) => sf.literal(String(n), nn(XSD_INTEGER))
    const desired: Quad[] = []
    // The arrows point at the first line added, else the first removed.
    const target = lines.find((l) => l.change === 'added') ?? lines.find((l) => l.change === 'removed')
    for (const l of lines) {
      const node = nn(`${m.iri}/data-${hash(l.key)}`)
      desired.push(
        sf.quad(s, nn(LIVE.dataLine), node),
        sf.quad(node, nn(RDF_TYPE), nn(LIVE.DataLine)),
        sf.quad(node, nn(LIVE.lineSubject), sf.literal(l.subject.text)),
        sf.quad(node, nn(LIVE.subjectToken), sf.literal(l.subject.token)),
        sf.quad(node, nn(LIVE.lineNode), sf.literal(hash(l.subject.text))),
        sf.quad(node, nn(LIVE.linePredicate), sf.literal(l.predicate.text)),
        sf.quad(node, nn(LIVE.predicateToken), sf.literal(l.predicate.token)),
        sf.quad(node, nn(LIVE.lineObject), sf.literal(l.object.text)),
        sf.quad(node, nn(LIVE.objectToken), sf.literal(l.object.token)),
        sf.quad(node, nn(LIVE.lineKey), sf.literal(l.sort)),
      )
      if (l.change) desired.push(sf.quad(node, nn(LIVE.lineChange), nn(l.change === 'added' ? LIVE.LineAdded : LIVE.LineRemoved)))
      if (l === target) desired.push(sf.quad(s, nn(LIVE.changedLine), node))
    }
    if (changed) desired.push(sf.quad(s, nn(LIVE.triplesAdded), integer(added.length)), sf.quad(s, nn(LIVE.triplesRemoved), integer(removed.length)))
    const dg = sf.defaultGraph()
    const current: Quad[] = []
    for (const p of [LIVE.changedLine, LIVE.triplesAdded, LIVE.triplesRemoved]) current.push(...site.match(s, nn(p), null, dg))
    for (const q of site.match(s, nn(LIVE.dataLine), null, dg)) current.push(q, ...site.match(q.object, null, null, dg))
    const keys = new Set(desired.map(quadKey))
    const had = new Set(current.map(quadKey))
    const delta: Delta = { remove: current.filter((q) => !keys.has(quadKey(q))), add: desired.filter((q) => !had.has(quadKey(q))) }
    if (!delta.add.length && !delta.remove.length) return
    this.runtime.apply(delta)
    this.opts.flush()
  }

  /** Carry out a copy request: the source shown, with the prefixes it uses; then withdraw the request. */
  private async copy(example: string, editor: string) {
    const source = this.sources(example).find((s) => s.editor === editor)
    if (source) {
      const write = this.opts.copy ?? ((t: string) => globalThis.navigator?.clipboard?.writeText(t))
      try {
        await write(completeSource(source.text))
      } catch (e) {
        console.warn('[semblance] could not copy', e)
      }
    }
    if (this.disposed) return
    const t = setTimeout(() => {
      this.timers.delete(t)
      if (this.disposed) return
      const f = this.runtime.store.factory
      const requests = this.runtime.store.match(f.namedNode(example), f.namedNode(LIVE.copied), null, f.defaultGraph())
      if (!requests.length) return
      this.runtime.apply({ remove: requests, add: [] })
      this.opts.flush()
    }, this.opts.copiedMs ?? 1500)
    this.timers.add(t)
  }
}

/** A term as Turtle writes it, and the kind of token it is (pname, iri, string, number, keyword), as code colours them. */
export interface DataTerm {
  text: string
  token: string
}

/** A line of a live example's data: its triple's terms, where it sorts, and what the last interaction did to it. */
export interface DataLine {
  key: string
  sort: string
  subject: DataTerm
  predicate: DataTerm
  object: DataTerm
  change?: 'added' | 'removed'
}

/**
 * The data, a line per triple: by subject, then rdf:type, rdfs:label and the other predicates,
 * then object; a removed triple (marked) in its place, beside what replaced it.
 */
export function dataLines(present: Map<string, Quad>, removed: Quad[], added: Set<string>, prefixes: Record<string, string>): DataLine[] {
  const rank = (p: string) => (p === RDF_TYPE ? '0' : p === NS.rdfs + 'label' ? '1' : '2')
  const rows = [
    ...[...present].map(([key, q]) => ({ key, q, change: added.has(key) ? ('added' as const) : undefined })),
    ...removed.map((q) => ({ key: quadKey(q), q, change: 'removed' as const })),
  ].map((r) => {
    const subject = dataTerm(r.q.subject, prefixes)
    const predicate = r.q.predicate.value === RDF_TYPE ? { text: 'a', token: 'keyword' } : dataTerm(r.q.predicate, prefixes)
    const object = dataTerm(r.q.object, prefixes)
    const sort = `${subject.text} ${rank(r.q.predicate.value)}${predicate.text} ${r.change === 'removed' ? 0 : 1}${object.text}`
    return { key: r.key, sort, subject, predicate, object, change: r.change }
  })
  return rows.sort((a, b) => (a.sort < b.sort ? -1 : a.sort > b.sort ? 1 : 0))
}

/** A term as Turtle writes it, with the given prefixes, and its kind of token. */
function dataTerm(t: Term, prefixes: Record<string, string>): DataTerm {
  if (t.termType === 'NamedNode') {
    const short = compactIri(t.value, prefixes)
    return short === t.value ? { text: `<${t.value}>`, token: 'iri' } : { text: short, token: 'pname' }
  }
  if (t.termType === 'BlankNode') return { text: `_:${t.value}`, token: 'pname' }
  if (t.termType !== 'Literal') return { text: t.value, token: 'string' }
  const dt = t.datatype?.value
  if (dt === XSD_BOOLEAN && /^(true|false)$/.test(t.value)) return { text: t.value, token: 'keyword' }
  if (dt === XSD_INTEGER && /^[+-]?\d+$/.test(t.value)) return { text: t.value, token: 'number' }
  const quoted = JSON.stringify(t.value)
  if (t.language) return { text: `${quoted}@${t.language}`, token: 'string' }
  if (!dt || dt === NS.xsd + 'string') return { text: quoted, token: 'string' }
  return { text: `${quoted}^^${dataTerm(t.datatype, prefixes).text}`, token: 'string' }
}

/** A short, stable name for a line's triple (FNV-1a, hex). */
function hash(text: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 0x01000193)
  return (h >>> 0).toString(16).padStart(8, '0')
}

/**
 * The rules of `css` that `site` does not have, each cascade layer's nested under `scope` (so a
 * sandbox's style rules style only its own elements), and the keyframes it adds.
 */
export function scopedCss(css: string, site: string, scope: string): string {
  const siteLines = new Set(site.split('\n'))
  const out: string[] = []
  const layer = /@layer sem\.(\w+) \{\n([\s\S]*?)\n\}/g
  for (const m of css.matchAll(layer)) {
    const added = m[2]!.split('\n').filter((l) => l.trim() && !siteLines.has(l))
    if (added.length) out.push(`@layer sem.${m[1]} {\n${scope} {\n${added.map((l) => l.replace(/^(\s*):root\b/, '$1&')).join('\n')}\n}\n}`)
  }
  for (const k of css.matchAll(/@keyframes [^{]+\{\n[\s\S]*?\n\}/g)) if (!site.includes(k[0])) out.push(k[0])
  return out.length ? ['@layer sem.tokens, sem.hooks, sem.components, sem.variants, sem.intents;', ...out].join('\n\n') : ''
}
