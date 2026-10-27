// Function rules and reactions for frames (windows, palettes), the desktop that hosts them,
// its taskbar (ontology/components/window.ttl), and progress bars.
import type { NamedNode, Quad, Term } from '@rdfjs/types'
import type { ReactionContext, ReactionFn } from '../behaviour/events.ts'
import type { FnRule, Out, RuleContext } from '../engine/context.ts'
import { BEH, C, DND, H, MOD, NS, RDF_FIRST, RDF_NIL, RDF_REST, RDF_TYPE, RDFS_SUBCLASS, SEM, XSD_BOOLEAN, XSD_INTEGER } from '../ns.ts'
import { mint, type Delta } from '../rdf.ts'
import { md5 } from './data.ts'
import { FB, hitMarks, phrase } from './feedback.ts'
import { gridTracks } from './layout.ts'
import { encodeForUri, named, RDFS_LABEL, str, text } from './util.ts'

const c = NS.c
/** Frame vocabulary (window.ttl) not in ns.ts. */
export const WIN = {
  Frame: c + 'Frame',
  Window: c + 'Window',
  Draggable: c + 'Draggable',
  Resizable: c + 'Resizable',
  Raisable: c + 'Raisable',
  Minimizable: c + 'Minimizable',
  Maximizable: c + 'Maximizable',
  Dockable: c + 'Dockable',
  windowState: c + 'windowState',
  Normal: c + 'Normal',
  Minimized: c + 'Minimized',
  Maximized: c + 'Maximized',
  attachedTo: c + 'attachedTo',
  NoEdge: c + 'NoEdge',
  StartEdge: c + 'StartEdge',
  EndEdge: c + 'EndEdge',
  TopEdge: c + 'TopEdge',
  BottomEdge: c + 'BottomEdge',
  snapDistance: c + 'snapDistance',
  throwTime: c + 'throwTime',
  z: c + 'z',
  topmost: c + 'topmost',
  minimizes: c + 'minimizes',
  maximizes: c + 'maximizes',
  restores: c + 'restores',
  undocks: c + 'undocks',
  activates: c + 'activates',
  opens: c + 'opens',
  Tab: c + 'Tab',
  tabs: c + 'tabs',
  selected: c + 'selected',
  detached: c + 'detached',
  detaches: c + 'detaches',
  Minimize: c + 'MinimizeCommand',
  Maximize: c + 'MaximizeCommand',
  Restore: c + 'RestoreCommand',
  Undock: c + 'UndockCommand',
  Move: c + 'MoveHandle',
  MoveHint: c + 'MoveHint',
  ReturnToTab: c + 'ReturnToTabCommand',
  Windows: c + 'WindowsRegion',
} as const
const PROV_DERIVED_FROM = 'http://www.w3.org/ns/prov#wasDerivedFrom'
/**
 * The edges a frame can be attached to, by slug, with the event detail that measures each and
 * which way along each axis (inline, block) is away from it (a velocity that way carries a
 * release further from the edge).
 */
const EDGES = [
  { iri: WIN.StartEdge, slug: 'start', distance: NS.beh + 'toStart', away: [1, 0] },
  { iri: WIN.EndEdge, slug: 'end', distance: NS.beh + 'toEnd', away: [-1, 0] },
  { iri: WIN.TopEdge, slug: 'top', distance: NS.beh + 'toTop', away: [0, 1] },
  { iri: WIN.BottomEdge, slug: 'bottom', distance: NS.beh + 'toBottom', away: [0, -1] },
] as const
const SNAP_DEFAULT = 24

const int = (ctx: RuleContext, s: Term, p: string, fallback: number) => {
  const v = Number(str(ctx.object(s, p)))
  return Number.isFinite(v) ? Math.round(v) : fallback
}
const isFalse = (t: Term | undefined) => t?.termType === 'Literal' && t.value === 'false'

/** Every class `cls` is a (reflexive, transitive) subclass of. */
function supers(match: (s: Term | string, p: string) => Term[], cls: string): Set<string> {
  const out = new Set([cls])
  const todo = [cls]
  while (todo.length) for (const sup of match(todo.pop()!, RDFS_SUBCLASS)) if (!out.has(sup.value)) out.add(sup.value), todo.push(sup.value)
  return out
}

/** The classes of a node, with their superclasses (the ontology part memoized, untraced). */
export function classesOf(ctx: RuleContext, node: Term): Set<string> {
  const out = new Set<string>()
  for (const t of ctx.objects(node, RDF_TYPE)) {
    const all = ctx.memo('frame:supers:' + t.value, () => ctx.untraced(() => supers((s, p) => ctx.objects(s, p), t.value)))
    for (const cls of all) out.add(cls)
  }
  return out
}

/** The same for reactions, which read the state before the event. */
function classesIn(ctx: ReactionContext, node: Term): Set<string> {
  const f = ctx.runtime.store.factory
  const out = new Set<string>()
  const objects = (s: Term | string, p: string) => ctx.match(typeof s === 'string' ? f.namedNode(s) : s, p, null).map((q) => q.object)
  for (const t of objects(node, RDF_TYPE)) for (const cls of supers(objects, t.value)) out.add(cls)
  return out
}

/** A frame's state, read once for its rendering and its taskbar entry. */
function stateOf(ctx: RuleContext, frame: Term) {
  const state = ctx.object(frame, WIN.windowState)
  const edgeNode = ctx.object(frame, WIN.attachedTo)
  const edge = EDGES.find((e) => e.iri === edgeNode?.value)
  const minimized = state?.value === WIN.Minimized
  const maximized = state?.value === WIN.Maximized
  return { state, edgeNode, edge: maximized ? undefined : edge, minimized, maximized }
}

const titleOf = (ctx: RuleContext, frame: Term) => text(ctx, frame, C.title) ?? text(ctx, frame, C.label) ?? 'Window'

/** The tab a window was minted from (c:Tabs records it), if any. */
function originTab(ctx: RuleContext, frame: Term): Term | undefined {
  const origin = ctx.object(frame, PROV_DERIVED_FROM)
  return origin && ctx.objects(origin, RDF_TYPE).some((t) => t.value === WIN.Tab) ? origin : undefined
}

/** A normal floating window on a desktop that tiles (c:DesktopGridLayout): the grid places it, not its geometry. */
function tiledOn(ctx: RuleContext, frame: NamedNode, caps: Set<string>, minimized: boolean, maximized: boolean, docked: boolean): boolean {
  if (!caps.has(WIN.Window) || minimized || maximized || docked) return false
  return ctx.match(null, C.hosts, frame).some(({ subject }) => ctx.object(subject, C.tileLayout)?.value === C.DesktopGridLayout)
}

/**
 * A frame: a title bar over a body, with the controls of the capabilities its instance has.
 * Closed (c:open false) it is an empty hidden root, and its body is not evaluated; minimized,
 * it keeps its parts under a hidden root (the body is parked, not dropped), and its desktop's
 * taskbar brings it back. Geometry is inline style; everything else is hooks for the theme.
 */
export const frame: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const text = ctx.modality === MOD.Text
  if (isFalse(ctx.object(focus, FB.open))) {
    o.renders(o.el('root', text ? 'Div' : 'Section', { hidden: '' }))
    return o.quads
  }
  const caps = classesOf(ctx, focus)
  const title = titleOf(ctx, focus)
  const { state, edge, edgeNode, minimized, maximized } = stateOf(ctx, focus)
  const body = ctx.object(focus, C.body)
  const content = named(body) ? [body] : []
  if (text) {
    if (minimized) {
      o.renders(o.el('root', 'Div', {}, [o.el('title', 'Div', {}, [o.text('title-text', `${symbolOf(ctx, state) ?? '▁'} ${title}`)])]))
      return o.quads
    }
    const mark = maximized ? symbolOf(ctx, state) : edge ? symbolOf(ctx, edgeNode) : undefined
    o.renders(o.el('root', 'Div', {}, [o.el('title', 'Div', {}, [o.text('title-text', `┌ ${title}${mark ? ' ' + mark : ''} ┐`)]), ...content]))
    return o.quads
  }
  const tiled = tiledOn(ctx, focus, caps, minimized, maximized, !!edge)
  const tab = originTab(ctx, focus)
  const hidden = minimized || coveredByMaximized(ctx, focus)
  const control = (local: string, glyph: string, label: string, marker: string) => {
    // data-sem-control names the control for the theme (close, minimize, maximize, restore, dock, undock).
    const b = o.el(local, 'Button', { type: 'button', class: 'sem-window-control', 'data-sem-control': local }, [o.text(local + '-text', glyph)])
    o.attr(b, 'aria-label', label)
    o.tip(b, label)
    o.t(b, marker, focus)
    return b
  }
  const controls: NamedNode[] = []
  if (tab) controls.push(control('dock', '⇲', `${phrase(ctx, WIN.ReturnToTab, 'Return to its tab')}: ${title}`, C.docks))
  if (caps.has(WIN.Minimizable)) controls.push(control('minimize', '–', `${phrase(ctx, WIN.Minimize, 'Minimize')} ${title}`, WIN.minimizes))
  if (caps.has(WIN.Maximizable)) {
    if (maximized) controls.push(control('restore', '❐', `${phrase(ctx, WIN.Restore, 'Restore')} ${title}`, WIN.restores))
    else controls.push(control('maximize', '□', `${phrase(ctx, WIN.Maximize, 'Maximize')} ${title}`, WIN.maximizes))
  }
  if (edge && caps.has(WIN.Dockable)) controls.push(control('undock', '◳', `${phrase(ctx, WIN.Undock, 'Float')} ${title}`, WIN.undocks))
  // A window minted from a tab closes back into it; any other frame is an overlay that closes.
  controls.push(control('close', '✕', `${phrase(ctx, FB.Close, 'Close')} ${title}`, tab ? C.docks : FB.closes))

  const bar = o.el('bar', 'Header', { class: 'sem-window-bar' }, [o.el('title', 'Strong', {}, [o.text('title-text', title)]), ...controls])
  if (caps.has(WIN.Draggable) && !maximized && !tiled) o.attr(bar, 'data-sem-gesture', 'move')
  const parts = [bar, o.el('body', 'Div', { class: 'sem-window-body' }, content)]
  if (caps.has(WIN.Resizable) && !maximized && !edge && !tiled) {
    const grip = o.el('grip', 'Div', { class: 'sem-window-grip' })
    o.attr(grip, 'data-sem-gesture', 'resize').attr(grip, 'aria-hidden', 'true')
    parts.push(grip)
  }
  o.renders(o.el('root', 'Section', { style: tiled ? undefined : geometry(ctx, focus, caps, maximized, edge?.slug), hidden: hidden ? '' : undefined }, parts))
  const root = o.node('root')
  o.attr(root, 'role', 'dialog').attr(root, 'aria-label', title)
  if (tiled) o.attr(root, 'data-sem-tiled', 'true')
  return o.quads
}

/**
 * Whether a maximized frame on one of its desktops is in front of this one (a higher c:z, or the
 * same and placed after it): then it cannot be seen, and is not shown, so what is in it (an
 * inspector following the pointer) is parked. Only the frames' states are read until one is
 * maximized, so raising costs no more.
 */
function coveredByMaximized(ctx: RuleContext, focus: NamedNode): boolean {
  for (const { subject: d } of ctx.match(null, C.hosts, focus)) {
    for (const other of ctx.objects(d, C.hosts)) {
      if (other.equals(focus) || ctx.object(other, WIN.windowState)?.value !== WIN.Maximized || isFalse(ctx.object(other, FB.open))) continue
      // In front: a higher c:z, or the same and placed after it (the desktop places frames in IRI order).
      const [mz, fz] = [int(ctx, other, WIN.z, 0), int(ctx, focus, WIN.z, 0)]
      if (mz > fz || (mz === fz && other.value > focus.value)) return true
    }
  }
  return false
}

/** Default geometry, staggered by the window's position among its siblings. */
const STAGGER = 28

/** Inline geometry: all of it floating, the docked dimension docked, none maximized; then the stacking order. */
function geometry(ctx: RuleContext, focus: NamedNode, caps: Set<string>, maximized: boolean, edge: string | undefined): string | undefined {
  const window = caps.has(WIN.Window)
  const index = int(ctx, focus, C.index, 0)
  const width = int(ctx, focus, C.width, window ? 520 : 240)
  // A palette is as tall as its content unless it says otherwise.
  const height = (ctx.object(focus, C.height) || window) ? int(ctx, focus, C.height, 440) : undefined
  const px = (p: string, v: number | undefined) => (v === undefined ? [] : [`${p}: ${v}px;`])
  const decls = maximized
    ? []
    : edge === 'start' || edge === 'end'
      ? px('width', width)
      : edge === 'top' || edge === 'bottom'
        ? px('height', height)
        : [...px('left', int(ctx, focus, C.x, 96 + index * STAGGER)), ...px('top', int(ctx, focus, C.y, 120 + index * STAGGER)), ...px('width', width), ...px('height', height)]
  const z = ctx.object(focus, WIN.z)
  if (z) decls.push(`z-index: ${int(ctx, focus, WIN.z, 0)};`)
  return decls.length ? decls.join(' ') : undefined
}

const symbolOf = (ctx: RuleContext, t: Term | undefined) => (t ? str(ctx.object(t, SEM.symbol)) : undefined)

export const progress: FnRule = (ctx, focus) => {
  const value = Number(str(ctx.object(focus, C.value)) ?? 0)
  const max = Number(str(ctx.object(focus, C.max)) ?? 1) || 1
  const label = text(ctx, focus, C.label) ?? ''
  const active = str(ctx.object(focus, C.active)) === 'true'
  const share = Math.max(0, Math.min(1, value / max))
  const o = ctx.out(focus)
  if (ctx.modality === MOD.Text) {
    const n = 20
    const done = Math.round(share * n)
    o.renders(o.el('root', 'Div', {}, [o.text('line', `[${'#'.repeat(done)}${'-'.repeat(n - done)}] ${label}`)]))
    return o.quads
  }
  const track = o.el('track', 'Span', { class: 'sem-track' }, [o.el('fill', 'I', { class: 'sem-fill' })])
  const status = o.el('status', 'Span', { class: 'sem-status' }, [o.text('status-text', label)])
  const root = o.el('root', 'Div', { hidden: active ? undefined : '', style: `--sem-progress: ${(share * 100).toFixed(1)}%` }, [track, status])
  o.attr(root, 'role', 'progressbar')
    .attr(root, 'aria-valuemin', '0')
    .attr(root, 'aria-valuemax', String(max))
    .attr(root, 'aria-valuenow', String(value))
    .attr(root, 'aria-valuetext', label)
  o.renders(root)
  return o.quads
}

const byIri = (a: Term, b: Term) => (a.value < b.value ? -1 : a.value > b.value ? 1 : 0)

/**
 * Places what other components asked to put here (c:hosts). Raisable frames share one
 * stacking layer, inside which their c:z orders them; dialogs and toasts stay above it.
 * A closed frame (c:open false) is not placed at all, so it costs nothing until it opens.
 */
export const desktop: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const hosted = ctx.objects(focus, C.hosts).filter(named).sort(byIri)
  const raisable = hosted.filter((n) => classesOf(ctx, n).has(WIN.Raisable))
  const frames = raisable.filter((n) => !isFalse(ctx.object(n, FB.open)))
  const others = hosted.filter((n) => !raisable.includes(n))
  if (ctx.modality === MOD.Text) {
    o.renders(o.el('root', 'Div', {}, hosted.filter((n) => !raisable.includes(n) || frames.includes(n))))
    return o.quads
  }
  // Tiled, the frames' layer takes c:Grid's parameters; the layout itself is a style hook (data-sem-tile-layout).
  const { style, columns } = ctx.object(focus, C.tileLayout)?.value === C.DesktopGridLayout ? gridTracks(ctx, focus) : {}
  const layer = o.el('frames', 'Div', { class: 'sem-frames', style }, frames)
  if (columns) o.attr(layer, 'data-columns', columns)
  const root = o.el('root', 'Div', {}, [layer, ...others])
  o.renders(root)
  return o.quads
}

/**
 * The open frames of a desktop, each an entry that restores, raises or minimizes it. A draggable
 * taskbar leads with a grip (the move handle, for the pointer and the arrow keys); placed by it, it
 * floats where it was put (c:x, c:y, kept on screen) or lies along the edge it is attached to.
 * Unplaced, the theme places it.
 */
export const taskbar: FnRule = (ctx, focus) => {
  const o = ctx.out(focus)
  const desktop = ctx.object(focus, C.of)
  const frames = named(desktop)
    ? ctx
        .objects(desktop, C.hosts)
        .filter(named)
        .filter((n) => classesOf(ctx, n).has(WIN.Frame) && !isFalse(ctx.object(n, FB.open)))
        .sort(byIri)
    : []
  const top = desktop ? ctx.object(desktop, WIN.topmost)?.value : undefined
  const label = text(ctx, focus, C.label) ?? phrase(ctx, WIN.Windows, 'Windows')
  const entries = frames.map((w) => {
    const s = stateOf(ctx, w)
    const symbol = (s.minimized || s.maximized ? symbolOf(ctx, s.state) : s.edge ? symbolOf(ctx, s.edgeNode) : undefined) ?? '▭'
    const slug = s.minimized ? 'minimized' : s.maximized ? 'maximized' : s.edge ? 'docked' : 'normal'
    const stateLabel = s.state ? (text(ctx, s.state, RDFS_LABEL) ?? slug) : undefined
    return { w, key: encodeForUri(w.value), title: titleOf(ctx, w), symbol, slug, stateLabel, active: w.value === top && !s.minimized }
  })
  const edgeNode = ctx.object(focus, WIN.attachedTo)
  const edge = EDGES.find((e) => e.iri === edgeNode?.value)
  if (ctx.modality === MOD.Text) {
    const line = entries.map((e) => `[${e.symbol} ${e.title}${e.active ? '*' : ''}]`).join(' ')
    const mark = edge ? ` ${symbolOf(ctx, edgeNode) ?? ''}`.trimEnd() : ''
    o.renders(o.el('root', 'Div', { hidden: entries.length ? undefined : '' }, [o.text('line', `▤ ${label}${mark}: ${line}`)]))
    return o.quads
  }
  const buttons = entries.map((e) => {
    const glyph = o.el(`task-${e.key}-icon`, 'Span', { class: 'sem-icon' }, [o.text(`task-${e.key}-icon-text`, e.symbol)])
    o.attr(glyph, 'aria-hidden', 'true')
    const b = o.el(`task-${e.key}`, 'Button', { type: 'button', class: 'sem-task' }, [glyph, o.el(`task-${e.key}-label`, 'Span', { class: 'sem-label' }, [o.text(`task-${e.key}-label-text`, e.title)])])
    o.attr(b, 'data-state', e.slug)
    if (e.active) o.attr(b, 'aria-current', 'true')
    if (e.stateLabel) o.tip(b, `${e.title} · ${e.stateLabel}`)
    o.t(b, WIN.activates, e.w)
    return b
  })
  const caps = classesOf(ctx, focus)
  const parts = [...buttons]
  if (caps.has(WIN.Draggable)) {
    // A focusable handle, not a <button> (a pointer pressed on a button is the button's): it moves
    // the taskbar at its own size, so one docked along an edge comes away as long as its entries.
    const grip = o.el('grip', 'Span', { class: 'sem-grip', tabindex: '0', role: 'button' })
    o.attr(grip, 'data-sem-gesture', 'move')
      .attr(grip, 'data-sem-move-size', 'content')
      .attr(grip, 'aria-label', `${phrase(ctx, WIN.Move, 'Move')} ${label}`)
      .attr(grip, 'aria-keyshortcuts', 'ArrowUp ArrowDown ArrowLeft ArrowRight')
    o.tip(grip, phrase(ctx, WIN.MoveHint, 'Drag, or use the arrow keys; at an edge it docks'))
    parts.unshift(grip)
  }
  const x = ctx.object(focus, C.x)
  const y = ctx.object(focus, C.y)
  // Floating where it was put, but never off screen: its grip stays within reach when the viewport shrinks.
  const placed = !edge && caps.has(WIN.Draggable) && x && y
  const style = placed ? `left: clamp(0px, ${int(ctx, focus, C.x, 0)}px, calc(100vw - 56px)); top: clamp(0px, ${int(ctx, focus, C.y, 0)}px, calc(100dvh - 48px));` : undefined
  const root = o.el('root', 'Div', { hidden: entries.length ? undefined : '', style }, parts)
  o.attr(root, 'role', 'toolbar').attr(root, 'aria-label', label)
  // Placed and floating: c:NoEdge's slug, as a docked one carries its edge's (the style layer's hook).
  if (placed) o.attr(root, 'data-sem-edge', 'none')
  if (edge?.slug === 'start' || edge?.slug === 'end') o.attr(root, 'aria-orientation', 'vertical')
  o.renders(root)
  return o.quads
}

export const containerFunctions: Record<string, FnRule> = { window: frame, progress, desktop, taskbar }

// --- reactions (function twins of the SPARQL in window.ttl) ----------------------------------------

const none = (): Delta => ({ add: [], remove: [] })
const merge = (...ds: Delta[]): Delta => ({ remove: ds.flatMap((d) => d.remove), add: ds.flatMap((d) => d.add) })
const nn = (ctx: ReactionContext, iri: string) => ctx.runtime.store.factory.namedNode(iri)
const integer = (ctx: ReactionContext, n: number) => ctx.runtime.store.factory.literal(String(n), nn(ctx, XSD_INTEGER))
const quad = (ctx: ReactionContext, s: Term, p: string, o: Term): Quad => ctx.runtime.store.factory.quad(s as NamedNode, nn(ctx, p), o as NamedNode)

/** Replace every `node p ?old` by `node p value` (or by nothing). */
function put(ctx: ReactionContext, node: Term, p: string, value: Term | undefined): Delta {
  return { remove: ctx.match(node, p, null), add: value ? [quad(ctx, node, p, value)] : [] }
}

/** The object of `marker` on the event's node or its nearest RDF/HTML ancestor that has one. */
function hitObject(ctx: ReactionContext, marker: string): Term | undefined {
  for (let node = ctx.object(ctx.event, BEH.node), hops = 0; node && hops < 32; hops++) {
    const hit = ctx.object(node, marker)
    if (hit) return hit
    node = ctx.match(null, H.hasChild, node)[0]?.subject
  }
  return undefined
}

/** Bring a frame to the front of the desktops hosting it, unless it already is. */
function raise(ctx: ReactionContext, w: Term): Delta {
  return merge(...ctx.match(null, C.hosts, w).map(({ subject: d }) => raiseOn(ctx, d, w)))
}

/** Bring a frame to the front of one desktop (it may not be placed there yet), unless it already is. */
function raiseOn(ctx: ReactionContext, d: Term, w: Term): Delta {
  const current = ctx.match(d, WIN.topmost, null)
  if (current.some((q) => q.object.equals(w))) return none()
  let top = 0
  for (const h of ctx.match(d, C.hosts, null)) for (const z of ctx.match(h.object, WIN.z, null)) top = Math.max(top, Number(z.object.value) || 0)
  return { remove: [...current, ...ctx.match(w, WIN.z, null)], add: [quad(ctx, d, WIN.topmost, w), quad(ctx, w, WIN.z, integer(ctx, top + 1))] }
}

const detach = (ctx: ReactionContext, tab: Term): Delta => ({ remove: [], add: [quad(ctx, tab, WIN.detached, ctx.runtime.store.factory.literal('true', nn(ctx, XSD_BOOLEAN)))] })

/** A window minted from a tab goes back into it: the tab is no longer detached, and is selected. */
function backToTab(ctx: ReactionContext): Delta {
  const tab = ctx.object(ctx.target, PROV_DERIVED_FROM)
  if (!tab || !ctx.match(tab, RDF_TYPE, nn(ctx, WIN.Tab)).length) return none()
  return merge({ remove: ctx.match(tab, WIN.detached, null), add: [] }, ...tabsOf(ctx, tab).map((tabs) => put(ctx, tabs, WIN.selected, tab)))
}

/**
 * The edge a move's release snaps a dockable frame to: the nearest within c:snapDistance of where
 * the release lands, carried c:throwTime ms along the gesture's velocity.
 */
function snapEdge(ctx: ReactionContext): string | undefined {
  const dockable = nn(ctx, WIN.Dockable)
  const limit = Number(ctx.object(dockable, WIN.snapDistance)?.value ?? SNAP_DEFAULT)
  const ahead = Number(ctx.object(dockable, WIN.throwTime)?.value ?? 0) / 1000
  const velocity = (p: string) => Number(ctx.object(ctx.event, NS.beh + p)?.value ?? 0) || 0
  const vi = velocity('inlineVelocity')
  const vb = velocity('blockVelocity')
  let best: { iri: string; d: number } | undefined
  for (const e of EDGES) {
    const at = ctx.object(ctx.event, e.distance)
    if (!at) continue
    const d = Number(at.value) + (vi * e.away[0] + vb * e.away[1]) * ahead
    if (d <= limit && (!best || d < best.d)) best = { iri: e.iri, d }
  }
  return best?.iri
}

const isState = (ctx: ReactionContext, w: Term, state: string) => ctx.match(w, WIN.windowState, nn(ctx, state)).length > 0
const setState = (ctx: ReactionContext, w: Term, state: string) => put(ctx, w, WIN.windowState, nn(ctx, state))
/** A control marked `marker $this` sets the target's window state. */
const stateControl = (marker: string, state: string): ReactionFn => (ctx) => (hitMarks(ctx, marker) ? setState(ctx, ctx.target, state) : none())

/** The c:Tabs whose c:tabs list holds `tab`. */
export function tabsOf(ctx: ReactionContext, tab: Term): Term[] {
  const out: Term[] = []
  for (const q of ctx.match(null, RDF_FIRST, tab)) {
    let head: Term = q.subject
    for (let hops = 0; hops < 10_000; hops++) {
      const prev: Quad | undefined = ctx.match(null, RDF_REST, head)[0]
      if (!prev) break
      head = prev.subject
    }
    for (const t of ctx.match(null, WIN.tabs, head)) out.push(t.subject)
  }
  return out
}

/** The cells of an RDF list from its head (rdf:nil ends it). */
export function cellsOf(ctx: ReactionContext, head: Term | undefined): Term[] {
  const cells: Term[] = []
  const seen = new Set<string>()
  for (let cell = head; cell && cell.value !== RDF_NIL && !seen.has(cell.value); cell = ctx.object(cell, RDF_REST)) seen.add(cell.value), cells.push(cell)
  return cells
}

/** Whether tabs hold a tab other than `tab`. */
export const holdsOther = (ctx: ReactionContext, tabs: Term, tab: Term) =>
  ctx.match(tabs, WIN.tabs, null).some((h) => cellsOf(ctx, h.object).some((cell) => ctx.match(cell, RDF_FIRST, null).some((q) => !q.object.equals(tab))))

/** The tab a drop carries, if it is one. */
const droppedTab = (ctx: ReactionContext) => {
  const tab = ctx.object(ctx.event, DND.carrying)
  return tab && ctx.match(tab, RDF_TYPE, nn(ctx, WIN.Tab)).length ? tab : undefined
}

/** Into these tabs: the tab, last (a new cell, <tabs>/~cell-<md5 of the tab>), and selected. */
const receive: ReactionFn = (ctx) => {
  const tab = droppedTab(ctx)
  if (!tab || !ctx.object(ctx.event, DND.into)?.equals(ctx.target)) return none()
  if (!tabsOf(ctx, tab).some((src) => !src.equals(ctx.target))) return none()
  const cell = nn(ctx, mint(ctx.target.value, 'cell-' + md5(tab.value)))
  const delta: Delta = { remove: ctx.match(ctx.target, WIN.selected, null), add: [quad(ctx, ctx.target, WIN.selected, tab)] }
  for (const h of ctx.match(ctx.target, WIN.tabs, null)) {
    if (h.object.value === RDF_NIL) delta.remove.push(h), delta.add.push(quad(ctx, ctx.target, WIN.tabs, cell))
    for (const c of cellsOf(ctx, h.object)) for (const q of ctx.match(c, RDF_REST, nn(ctx, RDF_NIL))) delta.remove.push(q), delta.add.push(quad(ctx, c, RDF_REST, cell))
  }
  delta.add.push(quad(ctx, cell, RDF_FIRST, tab), quad(ctx, cell, RDF_REST, nn(ctx, RDF_NIL)))
  return delta
}

/**
 * Out of the tabs it was in, when it goes into others or beside these (a zone that c:splitsAt an
 * edge), unless it is these tabs' only one: its cell unlinked, its selection there dropped, and
 * emptied tabs asked to close (c:CloseTile).
 */
const take: ReactionFn = (ctx) => {
  const tab = droppedTab(ctx)
  const into = ctx.object(ctx.event, DND.into)
  if (!tab || !into) return none()
  const beside = ctx.match(into, C.splitsAt, null).length > 0
  const delta = none()
  for (const first of ctx.match(null, RDF_FIRST, tab)) {
    const cell = first.subject
    const next = ctx.object(cell, RDF_REST)
    if (!next) continue
    for (const src of tabsOf(ctx, tab)) {
      if (!ctx.match(src, WIN.tabs, null).some((h) => cellsOf(ctx, h.object).some((c) => c.equals(cell)))) continue
      const only = !holdsOther(ctx, src, tab)
      if (!(beside ? !src.equals(ctx.target) || !only : into.equals(ctx.target) && !src.equals(ctx.target))) continue
      delta.remove.push(...ctx.match(cell, RDF_FIRST, tab), ...ctx.match(cell, RDF_REST, null), ...ctx.match(src, WIN.selected, tab))
      for (const h of ctx.match(src, WIN.tabs, cell)) delta.remove.push(h), delta.add.push(quad(ctx, src, WIN.tabs, next))
      for (const prev of ctx.match(null, RDF_REST, cell)) delta.remove.push(prev), delta.add.push(quad(ctx, prev.subject, RDF_REST, next))
      if (only) delta.add.push(quad(ctx, src, BEH.raise, nn(ctx, C.CloseTile)))
    }
  }
  return delta
}

export const reactions: Record<string, ReactionFn> = {
  'tabs:receive': receive,
  'tabs:take': take,
  'tabs:detach': (ctx) => {
    const tab = hitObject(ctx, WIN.detaches)
    return tab ? detach(ctx, tab) : none()
  },
  'tabs:detach-key': (ctx) => {
    const tab = hitObject(ctx, C.tearsOff)
    return tab ? detach(ctx, tab) : none()
  },
  // Detached into its window, placed where the gesture let go, floating, normal and in front.
  'tabs:tear-off': (ctx) => {
    const tab = hitObject(ctx, C.tearsOff)
    const w = hitObject(ctx, C.tearsInto)
    if (!tab || !w) return none()
    const x = ctx.object(ctx.event, NS.beh + 'x')
    const y = ctx.object(ctx.event, NS.beh + 'y')
    const off = ctx.match(w, WIN.windowState, null).filter((q) => q.object.value !== WIN.Normal)
    return merge(
      detach(ctx, tab),
      x && y ? merge(put(ctx, w, C.x, x), put(ctx, w, C.y, y)) : none(),
      { remove: ctx.match(w, WIN.attachedTo, null), add: [] },
      off.length ? { remove: off, add: [quad(ctx, w, WIN.windowState, nn(ctx, WIN.Normal))] } : none(),
      ...ctx.match(ctx.target, C.windowsOn, null).map((q) => raiseOn(ctx, q.object, w)),
    )
  },
  'window:dock': (ctx) => (hitMarks(ctx, C.docks) ? backToTab(ctx) : none()),
  'window:return': (ctx) => {
    const onto = ctx.object(ctx.event, BEH.onto)
    return onto && ctx.match(onto, C.gathers, ctx.target).length ? backToTab(ctx) : none()
  },
  'frame:move': (ctx) => {
    const x = ctx.object(ctx.event, NS.beh + 'x')
    const y = ctx.object(ctx.event, NS.beh + 'y')
    if (!x || !y || (classesIn(ctx, ctx.target).has(WIN.Dockable) && snapEdge(ctx))) return none()
    return merge(put(ctx, ctx.target, C.x, x), put(ctx, ctx.target, C.y, y))
  },
  'frame:snap': (ctx) => {
    const edge = snapEdge(ctx)
    return put(ctx, ctx.target, WIN.attachedTo, edge ? nn(ctx, edge) : undefined)
  },
  'frame:resize': (ctx) =>
    merge(put(ctx, ctx.target, C.width, ctx.object(ctx.event, NS.beh + 'width')), put(ctx, ctx.target, C.height, ctx.object(ctx.event, NS.beh + 'height'))),
  'frame:raise': (ctx) => raise(ctx, ctx.target),
  'frame:minimize': stateControl(WIN.minimizes, WIN.Minimized),
  'frame:maximize': stateControl(WIN.maximizes, WIN.Maximized),
  'frame:restore': stateControl(WIN.restores, WIN.Normal),
  'frame:undock': (ctx) => (hitMarks(ctx, WIN.undocks) ? put(ctx, ctx.target, WIN.attachedTo, undefined) : none()),
  'frame:open': (ctx) =>
    merge(
      ...ctx
        .match(ctx.target, WIN.opens, null)
        .map((q) => q.object)
        .filter((w) => classesIn(ctx, w).has(WIN.Raisable))
        .map((w) => merge(isState(ctx, w, WIN.Minimized) ? setState(ctx, w, WIN.Normal) : none(), raise(ctx, w))),
    ),
  'taskbar:restore': (ctx) => {
    const w = hitObject(ctx, WIN.activates)
    return w && isState(ctx, w, WIN.Minimized) ? setState(ctx, w, WIN.Normal) : none()
  },
  'taskbar:minimize': (ctx) => {
    const w = hitObject(ctx, WIN.activates)
    if (!w || isState(ctx, w, WIN.Minimized) || !classesIn(ctx, w).has(WIN.Minimizable)) return none()
    return ctx.match(null, WIN.topmost, w).length ? setState(ctx, w, WIN.Minimized) : none()
  },
  'taskbar:raise': (ctx) => {
    const w = hitObject(ctx, WIN.activates)
    return w ? raise(ctx, w) : none()
  },
}

export type { NamedNode, Out }
