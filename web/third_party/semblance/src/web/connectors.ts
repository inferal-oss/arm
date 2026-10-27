// The host side of connectors (ontology/components/connector.ttl): where their ends are, which
// only the page knows. The graph says what a connector joins (data-sem-from, data-sem-to: its ends'
// ids), how it runs (data-sem-route, data-sem-leader), which ends have heads (data-sem-heads) and when
// it shows (data-sem-shown-while); this effect measures the ends' elements and writes the paths of
// each connector's SVG canvas, relative to the canvas, so the arrow stays attached while they move.
//
//   - Cheap: at most one frame's work per frame (reads first, then writes), only when something may
//     have moved - a change in the page, a scroll, a resize of the window, a canvas or an end, a
//     transition or an animation running - and only paths that changed are written. With no
//     connector on the page it does nothing at all.
//   - Never into the graph: geometry stays in the DOM. What it says is coarse and on the canvas
//     only: data-sem-drawn once both ends are found and can be seen.
//   - An end that is not rendered, hidden, out of view or covered (a sticky bar, a diagram that
//     stays on top) hides its connector.
//   - Colours are the theme's (classes, the connector's tone); a new connector draws itself in
//     unless motion is calmed (mot:preference, src/layers/motion.ts); routes that
//     go out to a side (c:Arc) go to the inline end, mirrored right to left.

import { motionCalmedAt } from '../layers/motion.ts'

const LAYOUT_PROPERTIES = /^(transform|translate|scale|rotate|top|left|right|bottom|inset|width|height|max-|min-|margin|padding|flex|grid|font|line-height|letter-spacing|border-width|all)/

type Pt = [number, number]
interface Box {
  left: number
  top: number
  right: number
  bottom: number
  width: number
  height: number
}

/** What one connector's canvas should show: its parts' attributes, or nothing (hidden). */
interface Plan {
  line: string
  leader?: string
  start?: Pt
  headTo?: string
  headFrom?: string
}

export interface ConnectorStats {
  /** Frames that measured connectors. */
  frames: number
  /** Attribute writes on connectors (paths, visibility). */
  writes: number
}

export interface ConnectorHost {
  /** Measure again at the next frame (e.g. after the renderer patched the page). */
  update(): void
  /** Measure and draw now (tests; a host that knows layout just changed). */
  draw(): void
  readonly stats: ConnectorStats
  dispose(): void
}

export interface ConnectorOptions {
  /** How to wait for the next frame (requestAnimationFrame by default). */
  frame?: (cb: () => void) => () => void
}

export function attachConnectors(host: HTMLElement, opts: ConnectorOptions = {}): ConnectorHost {
  const doc = host.ownerDocument
  const win = doc.defaultView!
  // The connectors on the page, kept from what is added (a scan of each added subtree only).
  const live = new Set<SVGSVGElement>()
  const collect = (n: Node) => {
    if (n.nodeType !== 1) return
    const el = n as Element
    if (el.classList?.contains('sem-connector')) live.add(el as SVGSVGElement)
    for (const c of el.getElementsByClassName?.('sem-connector') ?? []) live.add(c as SVGSVGElement)
  }
  collect(host)
  const stats: ConnectorStats = { frames: 0, writes: 0 }
  const frame =
    opts.frame ??
    ((cb: () => void) => {
      if (win.requestAnimationFrame) {
        const id = win.requestAnimationFrame(cb)
        return () => win.cancelAnimationFrame(id)
      }
      const id = win.setTimeout(cb, 16)
      return () => win.clearTimeout(id)
    })
  let cancel: (() => void) | undefined
  const schedule = () => {
    if (!cancel && live.size) cancel = frame(run)
  }

  // --- what may move things ---------------------------------------------------------------------

  /** Transitions and animations running now: while any is, every frame is measured. */
  const moving = new Set<EventTarget>()
  let lastStart = 0
  const started = (e: Event) => {
    if (e.type.startsWith('transition') && !LAYOUT_PROPERTIES.test((e as TransitionEvent).propertyName ?? '')) return
    if (!live.size || !e.target) return
    moving.add(e.target)
    lastStart = Date.now()
    schedule()
  }
  const ended = (e: Event) => {
    if (e.target && moving.delete(e.target)) schedule()
  }

  /** Our own writes (a connector's parts, its data-sem-drawn) move nothing. */
  const own = (r: MutationRecord) => {
    if (r.type !== 'attributes') return false
    const el = r.target as Element
    if (el.classList?.contains('sem-connector')) return r.attributeName === 'data-sem-drawn'
    return !!(el.parentNode as Element | null)?.classList?.contains('sem-connector')
  }
  const mutations = new win.MutationObserver((records) => {
    let moved = false
    for (const r of records) {
      if (r.type === 'childList') r.addedNodes.forEach(collect)
      else if (r.type === 'attributes' && r.attributeName === 'class') collect(r.target)
      if (!moved && !own(r)) moved = true
    }
    if (moved) schedule()
  })
  mutations.observe(host, { subtree: true, childList: true, attributes: true, characterData: true })
  const resizes = typeof win.ResizeObserver === 'function' ? new win.ResizeObserver(() => schedule()) : undefined
  const observed = new Set<Element>()

  const pointerWatch = () => {
    for (const c of live) if (c.hasAttribute('data-sem-shown-while')) return schedule()
  }
  const listeners: [EventTarget, string, EventListener, AddEventListenerOptions][] = [
    [win, 'scroll', schedule, { passive: true, capture: true }],
    [win, 'resize', schedule, { passive: true }],
    [host, 'pointerover', pointerWatch, { passive: true }],
    [host, 'pointerout', pointerWatch, { passive: true }],
    [host, 'focusin', pointerWatch, {}],
    [host, 'focusout', pointerWatch, {}],
    ...(['transitionrun', 'animationstart'] as const).map((t) => [host, t, started, { passive: true }] as [EventTarget, string, EventListener, AddEventListenerOptions]),
    ...(['transitionend', 'transitioncancel', 'animationend', 'animationcancel'] as const).map((t) => [host, t, ended, { passive: true }] as [EventTarget, string, EventListener, AddEventListenerOptions]),
  ]
  for (const [t, type, fn, o] of listeners) t.addEventListener(type, fn, o)

  // --- finding the ends -----------------------------------------------------------------------------

  const cache = new WeakMap<Element, Map<string, Element | null>>()
  /** The element an id names, in the connector's own sandbox (a live example's ids may repeat the site's). */
  function resolve(svg: Element, attr: string): Element | null {
    const id = svg.getAttribute(attr)
    if (!id) return null
    let byId = cache.get(svg)
    if (!byId) cache.set(svg, (byId = new Map()))
    const had = byId.get(attr)
    if (had && had.isConnected && had.id === id) return had
    const scope = svg.closest('[data-sem-sandbox]')
    let el = doc.getElementById(id)
    if (el && el.closest('[data-sem-sandbox]') !== scope) el = (scope ?? host).querySelector(`[id="${id.replace(/["\\]/g, '\\$&')}"]`)
    byId.set(attr, el)
    return el
  }

  const shown = (el: Element) => !el.closest('[hidden]') && (typeof el.getClientRects !== 'function' || el.getClientRects().length > 0)
  const pointed = (el: Element) => {
    try {
      return el.matches(':hover') || el.matches(':focus-within')
    } catch {
      return false
    }
  }
  const reduced = motionCalmedAt
  const rtl = (el: Element) => {
    const dir = el.closest('[dir]')?.getAttribute('dir')
    if (dir) return dir === 'rtl'
    return win.getComputedStyle?.(el).direction === 'rtl'
  }

  /** Whether an end can be seen: in the viewport, and not under something else (hit-tested where it is). */
  function seen(el: Element, r: Box): boolean {
    const vw = win.innerWidth || doc.documentElement.clientWidth
    const vh = win.innerHeight || doc.documentElement.clientHeight
    if (r.width <= 0 && r.height <= 0) return false
    if (vw && vh && (r.bottom <= 0 || r.right <= 0 || r.top >= vh || r.left >= vw)) return false
    if (typeof doc.elementFromPoint !== 'function') return true
    const x = (Math.max(r.left, 0) + Math.min(r.right, vw || r.right)) / 2
    const ys = [(Math.max(r.top, 0) + Math.min(r.bottom, vh || r.bottom)) / 2, Math.min(r.bottom, vh || r.bottom) - 1]
    for (const y of ys) {
      const hit = doc.elementFromPoint(x, y)
      if (!hit || hit === el || el.contains(hit) || hit.contains(el)) return true
    }
    return false
  }

  // --- a frame: measure everything, then write what changed -----------------------------------

  const drawn = new WeakSet<Element>()
  function plan(svg: SVGSVGElement, used: Set<Element>): Plan | undefined {
    if (!shown(svg)) return undefined
    const from = resolve(svg, 'data-sem-from')
    const to = resolve(svg, 'data-sem-to')
    if (!from || !to) return undefined
    used.add(svg).add(from).add(to)
    if (!shown(from) || !shown(to)) return undefined
    if (svg.getAttribute('data-sem-shown-while') === 'pointed' && !pointed(from)) return undefined
    const a = from.getBoundingClientRect()
    const b = to.getBoundingClientRect()
    if (!seen(from, a) || !seen(to, b)) return undefined
    const o = svg.getBoundingClientRect()
    const leaderEl = resolve(svg, 'data-sem-leader')
    if (leaderEl) used.add(leaderEl)
    const shift = (r: DOMRect | Box): Box => ({ left: r.left - o.left, top: r.top - o.top, right: r.right - o.left, bottom: r.bottom - o.top, width: r.width, height: r.height })
    return route({
      a: shift(a),
      b: shift(b),
      leader: leaderEl ? shift(leaderEl.getBoundingClientRect()) : undefined,
      route: svg.getAttribute('data-sem-route') ?? 'curved',
      heads: svg.getAttribute('data-sem-heads') ?? 'to',
      roundFrom: round(from),
      roundTo: round(to),
      rtl: rtl(svg),
    })
  }

  const set = (el: Element | null | undefined, name: string, value: string) => {
    if (!el || el.getAttribute(name) === value) return
    el.setAttribute(name, value)
    stats.writes++
  }
  function apply(svg: SVGSVGElement, p: Plan | undefined) {
    const part = (sel: string) => [...svg.children].find((c) => c.matches(sel))
    if (!p) {
      if (svg.hasAttribute('data-sem-drawn')) svg.removeAttribute('data-sem-drawn'), stats.writes++
      drawn.delete(svg)
      return
    }
    const line = part('.sem-connector-line')
    set(line, 'd', p.line)
    if (p.leader !== undefined) set(part('.sem-connector-leader'), 'd', p.leader)
    if (p.start) {
      const dot = part('.sem-connector-start')
      set(dot, 'cx', p.start[0].toFixed(1))
      set(dot, 'cy', p.start[1].toFixed(1))
      set(dot, 'r', '2.5')
    }
    if (p.headTo) set(part('.sem-connector-head[data-sem-end=to]'), 'd', p.headTo)
    if (p.headFrom) set(part('.sem-connector-head[data-sem-end=from]'), 'd', p.headFrom)
    if (!svg.hasAttribute('data-sem-drawn')) svg.setAttribute('data-sem-drawn', ''), stats.writes++
    // A connector that appears draws itself in, unless motion is calmed.
    if (!drawn.has(svg)) {
      drawn.add(svg)
      const path = line as SVGPathElement | null
      if (path && svg.getAttribute('data-sem-line') !== 'dashed' && !reduced(svg) && typeof path.animate === 'function') {
        const length = path.getTotalLength?.() ?? 0
        if (length) path.animate([{ strokeDasharray: `${length}`, strokeDashoffset: `${length}` }, { strokeDasharray: `${length}`, strokeDashoffset: '0' }], { duration: 380, easing: 'cubic-bezier(.2,.7,.2,1)' })
      }
    }
  }

  function run() {
    cancel = undefined
    for (const c of live) if (!c.isConnected) live.delete(c)
    if (!live.size) return
    stats.frames++
    const used = new Set<Element>()
    const connectors = [...live]
    const plans = connectors.map((svg) => plan(svg, used))
    connectors.forEach((svg, i) => apply(svg, plans[i]))
    if (resizes) {
      for (const el of used) if (!observed.has(el)) resizes.observe(el), observed.add(el)
      for (const el of observed) if (!used.has(el)) resizes.unobserve(el), observed.delete(el)
    }
    for (const t of moving) if (!(t as Node).isConnected) moving.delete(t)
    if (moving.size && Date.now() - lastStart > 4000) moving.clear()
    if (moving.size) schedule()
  }

  schedule()
  return {
    update: schedule,
    draw() {
      cancel?.()
      run()
    },
    stats,
    dispose() {
      mutations.disconnect()
      resizes?.disconnect()
      for (const [t, type, fn, o] of listeners) t.removeEventListener(type, fn, o)
      cancel?.()
    },
  }
}

/** A round end (a diagram's dot): the line stops at its ring, not its box. */
const round = (el: Element) => el.localName === 'circle' || el.localName === 'ellipse'

const f = (n: number) => n.toFixed(1)
const center = (r: Box): Pt => [r.left + r.width / 2, r.top + r.height / 2]
const unit = (from: Pt, to: Pt): Pt => {
  const dx = to[0] - from[0]
  const dy = to[1] - from[1]
  const l = Math.hypot(dx, dy) || 1
  return [dx / l, dy / l]
}

/** Where a ray from a box's centre towards a point leaves it (its ring, for a round end). */
function rim(r: Box, toward: Pt, isRound: boolean): Pt {
  const c = center(r)
  const [ux, uy] = unit(c, toward)
  if (isRound) {
    const radius = r.width / 2 + 3
    return [c[0] + ux * radius, c[1] + uy * radius]
  }
  const tx = ux ? r.width / 2 / Math.abs(ux) : Infinity
  const ty = uy ? r.height / 2 / Math.abs(uy) : Infinity
  const t = Math.min(tx, ty)
  return [c[0] + ux * t, c[1] + uy * t]
}

/** A small arrowhead with its tip at `tip`, coming in along `dir`. */
function head(tip: Pt, dir: Pt): string {
  const [x, y] = tip
  const [ux, uy] = dir
  return `M${f(x)},${f(y)} L${f(x - ux * 8 - uy * 4.5)},${f(y - uy * 8 + ux * 4.5)} L${f(x - ux * 8 + uy * 4.5)},${f(y - uy * 8 - ux * 4.5)} Z`
}

interface RouteInput {
  a: Box
  b: Box
  leader?: Box
  route: string
  heads: string
  roundFrom: boolean
  roundTo: boolean
  rtl: boolean
}

/** A connector's paths between two boxes (in the canvas's coordinates). Exported for tests. */
export function route(r: RouteInput): Plan {
  const { a, b } = r
  // Which side the to end is on: right of the from end (1), left (-1), or neither (0).
  const side = b.left >= a.right ? 1 : b.right <= a.left ? -1 : 0
  let points: Pt[] | undefined
  let cubic: [Pt, Pt, Pt, Pt] | undefined
  let leader: string | undefined
  let start: Pt | undefined
  if (r.leader && r.route !== 'straight' && r.route !== 'elbow') {
    // Out of running text: along the bottom of the phrase to the box's edge on the to end's side
    // (or, the to end above, the nearer side, then up), then a curve into the to end's centre.
    const L = r.leader
    const toward = center(b)[0] >= center(a)[0] ? 1 : -1
    const s = b.left > a.right ? 1 : b.right < a.left ? -1 : b.bottom < a.top ? toward : 0
    const end = center(b)
    if (s) {
      start = [s > 0 ? a.right : a.left, a.bottom]
      const edge = s > 0 ? L.right : L.left
      const from: Pt = [s > 0 ? Math.max(edge, start[0] + 6) : Math.min(edge, start[0] - 6), a.bottom]
      leader = `M${f(start[0])},${f(start[1])} L${f(from[0])},${f(from[1])}`
      const k = Math.max(40, Math.abs(end[0] - from[0]) * 0.5)
      const c1: Pt = [from[0] + s * k, from[1]]
      const c2: Pt = [end[0] - s * k * 0.6, end[1]]
      cubic = [from, c1, c2, r.roundTo ? rim(b, c2, true) : rim(b, c2, false)]
    } else {
      const up = b.bottom < a.top
      start = [center(a)[0], up ? a.top - 2 : a.bottom + 2]
      leader = ''
      const v = end[1] < start[1] ? -1 : 1
      const k = Math.max(40, Math.abs(end[1] - start[1]) * 0.5)
      const c1: Pt = [start[0], start[1] + v * k]
      const c2: Pt = [end[0], end[1] - v * k * 0.6]
      cubic = [start, c1, c2, rim(b, c2, r.roundTo)]
    }
  } else if (r.route === 'straight') {
    const p0 = rim(a, center(b), r.roundFrom)
    const p1 = rim(b, center(a), r.roundTo)
    cubic = [p0, p0, p1, p1]
  } else if (r.route === 'elbow') {
    if (side) {
      const p0: Pt = [side > 0 ? a.right : a.left, center(a)[1]]
      const p1: Pt = [side > 0 ? b.left : b.right, center(b)[1]]
      const mx = (p0[0] + p1[0]) / 2
      points = [p0, [mx, p0[1]], [mx, p1[1]], p1]
    } else {
      const v = center(b)[1] >= center(a)[1] ? 1 : -1
      const p0: Pt = [center(a)[0], v > 0 ? a.bottom : a.top]
      const p1: Pt = [center(b)[0], v > 0 ? b.top : b.bottom]
      const my = (p0[1] + p1[1]) / 2
      points = [p0, [p0[0], my], [p1[0], my], p1]
    }
  } else if (r.route === 'arc' && !side) {
    // Out of the inline end side of one and back into the same side of the other.
    const s = r.rtl ? -1 : 1
    const edge = (x: Box) => (s > 0 ? x.right - 8 : x.left + 8)
    const p0: Pt = [edge(a), center(a)[1]]
    const p1: Pt = [edge(b), center(b)[1]]
    const k = 14 + Math.abs(p1[1] - p0[1]) / 6
    cubic = [p0, [p0[0] + s * k, p0[1]], [p1[0] + s * k, p1[1]], p1]
  } else if (side) {
    // Curved, side by side: out of the facing sides.
    const p0: Pt = [side > 0 ? a.right : a.left, center(a)[1]]
    const p1: Pt = [side > 0 ? b.left : b.right, center(b)[1]]
    const k = Math.max(24, Math.abs(p1[0] - p0[0]) * 0.5)
    cubic = [r.roundFrom ? rim(a, [p0[0] + side * k, p0[1]], true) : p0, [p0[0] + side * k, p0[1]], [p1[0] - side * k, p1[1]], r.roundTo ? rim(b, [p1[0] - side * k, p1[1]], true) : p1]
  } else {
    // Curved, one above the other: out of the facing edges.
    const v = center(b)[1] >= center(a)[1] ? 1 : -1
    const p0: Pt = [center(a)[0], v > 0 ? a.bottom : a.top]
    const p1: Pt = [center(b)[0], v > 0 ? b.top : b.bottom]
    const k = Math.max(24, Math.abs(p1[1] - p0[1]) * 0.5)
    cubic = [p0, [p0[0], p0[1] + v * k], [p1[0], p1[1] - v * k], p1]
  }
  let line: string
  let inTo: Pt
  let outFrom: Pt
  let tipTo: Pt
  let tipFrom: Pt
  if (points) {
    line = `M${points.map((p) => `${f(p[0])},${f(p[1])}`).join(' L')}`
    tipTo = points[points.length - 1]!
    tipFrom = points[0]!
    inTo = unit(points[points.length - 2]!, tipTo)
    outFrom = unit(points[1]!, tipFrom)
  } else {
    const [p0, c1, c2, p1] = cubic!
    line = `M${f(p0[0])},${f(p0[1])} C${f(c1[0])},${f(c1[1])} ${f(c2[0])},${f(c2[1])} ${f(p1[0])},${f(p1[1])}`
    tipTo = p1
    tipFrom = p0
    inTo = unit(c2[0] === p1[0] && c2[1] === p1[1] ? p0 : c2, p1)
    outFrom = unit(c1[0] === p0[0] && c1[1] === p0[1] ? p1 : c1, p0)
  }
  const plan: Plan = { line }
  if (leader !== undefined) plan.leader = leader
  if (start) plan.start = start
  if (r.heads === 'to' || r.heads === 'both') plan.headTo = head(tipTo, inTo)
  if (r.heads === 'from' || r.heads === 'both') plan.headFrom = head(tipFrom, outFrom)
  return plan
}
