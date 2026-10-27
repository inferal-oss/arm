// The host side of the drag and drop layer (ontology/layers/dragdrop.ttl): the pointer gesture
// and the keyboard drag. Both only say what happened - picked up, over this zone and position,
// put down, abandoned - as RDF events; the drag state, each zone's reception and the drop's
// position are the graph's (the layer's reactions and rules). While dragging, the view changes
// only transiently: the lifted element follows the pointer, and the page scrolls near its edges.
import type { Term } from '@rdfjs/types'
import { DND, NS } from '../ns.ts'
import { mint, owningFocus } from '../rdf.ts'
import type { DomRenderer } from '../render/dom.ts'
import { comboOf } from '../layers/keyboard.ts'
import { receptionOf } from '../layers/dragdrop.ts'
import type { EventBus, EventDetail, Handled } from './events.ts'

/** What a drop event copies from the drag in progress. */
const COPIED = [DND.carrying, DND.source, DND.from, DND.dropTarget, DND.into, DND.after, DND.before, DND.rank, DND.effect, DND.by]
/** Pixels a mouse or pen moves before a press becomes a drag; a touch lifts after a still, long press. */
const THRESHOLD = 5
const TOUCH_SLOP = 10
const LONG_PRESS = 350
/** How near an edge of the viewport or a scrolling container autoscroll starts, and its fastest step. */
const EDGE = 48
const MAX_STEP = 14

interface Place {
  zone: HTMLElement
  target: string
  receptacle: string
  after?: string
  before?: string
}

interface Session {
  mode: 'pointer' | 'keyboard'
  draggable: string
  el: HTMLElement
  carried: string
  /** Where it was picked up (dropping it there again changes nothing). */
  origin?: Place
  place?: Place
  /** Keyboard: the zone and slot (index among the zone's other draggables). */
  slot?: { zone: number; index: number }
  end: () => void
}

const placeKey = (p?: Place) => (p ? [p.target, p.receptacle, p.after ?? '', p.before ?? ''].join(' ') : '')

export class DragHost {
  private session?: Session
  private live?: HTMLElement
  private readonly doc: Document

  constructor(
    private readonly bus: EventBus,
    private readonly renderer: DomRenderer,
    private readonly onHandled: (h: Handled) => void,
  ) {
    this.doc = renderer.host.ownerDocument
  }

  private get f() {
    return this.bus.runtime.store.factory
  }

  /** The keys an event class is for (beh:key), e.g. what starts and drops a keyboard drag. */
  private keys(type: string): string[] {
    return this.bus.runtime.store.match(this.f.namedNode(type), this.f.namedNode(NS.beh + 'key'), null).map((q) => q.object.value)
  }

  private emit(type: string, focus: string, detail: EventDetail = {}): Handled {
    const handled = this.bus.emit(type, focus, detail)
    if (handled.recorded || handled.delta.add.length || handled.delta.remove.length) this.onHandled(handled)
    this.say()
    return handled
  }

  /** The live region says the lifted draggable's status (dnd:status, rendered as data-sem-drag-status). */
  private say() {
    const s = this.session
    const status = s && new Map(this.renderer.index.attributes(mint(s.draggable, 'root'))).get('data-sem-drag-status')
    if (!this.live && status === undefined) return
    if (!this.live) {
      this.live = this.doc.createElement('div')
      this.live.setAttribute('role', 'status')
      this.live.setAttribute('aria-live', 'assertive')
      this.live.setAttribute('data-sem-drag-live', '')
      this.doc.body.appendChild(this.live)
    }
    if (this.live.textContent !== (status ?? '')) this.live.textContent = status ?? ''
  }

  /** The component a draggable element is the root of. */
  private draggableOf(el: Element): string | undefined {
    const key = this.renderer.keyOf(el)
    return key && this.renderer.index.focusOf(key)
  }

  private zoneOf(el: Element | null): HTMLElement | undefined {
    const zone = el?.closest?.('[data-sem-drop-zone]') as HTMLElement | null
    return zone && this.renderer.host.contains(zone) && !zone.closest('[hidden], [inert]') ? zone : undefined
  }

  /** The draggables placed in a zone (not in a zone nested in it), but the one held. */
  private itemsOf(zone: HTMLElement): HTMLElement[] {
    const held = this.session?.el
    return ([...zone.querySelectorAll('[data-sem-drag]')] as HTMLElement[]).filter((d) => d !== held && this.zoneOf(d.parentElement) === zone)
  }

  private placeIn(zone: HTMLElement, index: number): Place {
    const key = this.renderer.keyOf(zone)!
    const place: Place = { zone, target: owningFocus(key), receptacle: zone.getAttribute('data-sem-drop-zone')! }
    if (zone.hasAttribute('data-sem-drop-ordered')) {
      const items = this.itemsOf(zone)
      place.after = items[index - 1]?.getAttribute('data-sem-drag') ?? undefined
      place.before = items[index]?.getAttribute('data-sem-drag') ?? undefined
    }
    return place
  }

  /** The zone and position under a point: before the first draggable whose middle is past it. */
  private placeAt(x: number, y: number): Place | undefined {
    const zone = this.zoneOf(this.doc.elementFromPoint?.(x, y) ?? null)
    if (!zone) return undefined
    const items = this.itemsOf(zone)
    const rects = items.map((d) => d.getBoundingClientRect())
    // In a row (the first two side by side) by x - mirrored right to left - else by y.
    const row = rects.length > 1 && Math.abs(rects[0]!.top - rects[1]!.top) < 2 && rects[0]!.left !== rects[1]!.left
    const rtl = this.doc.documentElement.dir === 'rtl'
    const past = (r: DOMRect) => (row ? (rtl ? x > r.left + r.width / 2 : x < r.left + r.width / 2) : y < r.top + r.height / 2)
    const index = rects.findIndex(past)
    return this.placeIn(zone, index < 0 ? items.length : index)
  }

  /** Tell the graph where the drag is (only when that changed). */
  private over(place: Place | undefined) {
    const s = this.session!
    if (placeKey(place) === placeKey(s.place)) return
    s.place = place
    const nn = (iri: string) => this.f.namedNode(iri)
    const about: [string, Term][] = []
    if (place) {
      about.push([DND.dropTarget, nn(place.target)], [DND.into, nn(place.receptacle)])
      if (place.after) about.push([DND.after, nn(place.after)])
      if (place.before) about.push([DND.before, nn(place.before)])
    }
    this.emit(DND.Over, s.draggable, { about })
  }

  /** Pick up a draggable element (an RDF dnd:Start); returns the session or nothing. */
  private lift(el: HTMLElement, mode: Session['mode'], copy: boolean, end: () => void): Session | undefined {
    const draggable = this.draggableOf(el)
    const carried = el.getAttribute('data-sem-drag')
    if (!draggable || !carried) return undefined
    const zone = this.zoneOf(el.parentElement)
    const s: Session = { mode, draggable, el, carried, end }
    this.session = s
    if (zone) {
      const all = ([...zone.querySelectorAll('[data-sem-drag]')] as HTMLElement[]).filter((d) => this.zoneOf(d.parentElement) === zone)
      s.origin = this.placeIn(zone, Math.max(0, all.indexOf(el)))
    }
    const nn = (iri: string) => this.f.namedNode(iri)
    const about: [string, Term][] = [[DND.by, nn(mode === 'pointer' ? DND.Pointer : DND.Keyboard)]]
    if (s.origin) about.push([DND.from, nn(s.origin.receptacle)])
    if (copy) about.push([DND.effect, nn(DND.Copy)])
    this.emit(DND.Start, draggable, { about })
    return s
  }

  /**
   * Put it down where it is: a drop the target accepts is a dnd:Drop addressed to the target,
   * carrying the drag (its reactions decide what it means); one it refuses is a dnd:Refusal,
   * with the reason. Away from every target, or where it came from, nothing happens.
   */
  private release() {
    const s = this.session
    if (!s) return
    const place = s.place
    if (!place || placeKey(place) === placeKey(s.origin)) return this.cancel()
    const store = this.bus.runtime.store
    const nn = (iri: string) => this.f.namedNode(iri)
    const current = nn(DND.Current)
    const about = COPIED.flatMap((p) => store.match(current, nn(p), null, this.f.defaultGraph()).map((q) => [p, q.object] as [string, Term]))
    const reception = nn(receptionOf(place.target, place.receptacle))
    const graph = this.bus.projection.engine.graph
    const refused = store.match(reception, nn(DND.verdict), nn(DND.Refused), graph).length > 0
    if (refused) {
      const reason = store.match(reception, nn(DND.reason), null, graph)[0]?.object
      this.emit(DND.Refusal, s.draggable, { about: reason ? [...about, [DND.reason, reason]] : about })
    } else this.emit(DND.Drop, place.target, { about })
    this.finish(place.zone)
  }

  private cancel() {
    const s = this.session
    if (!s) return
    this.emit(DND.Cancel, s.draggable)
    this.finish(s.origin?.zone)
  }

  /** The drag is over (dnd:End); a keyboard drag keeps the focus on what it carried. */
  private finish(zone?: HTMLElement) {
    const s = this.session!
    s.end()
    this.emit(DND.End, DND.Current)
    this.session = undefined
    this.say()
    if (s.mode !== 'keyboard') return
    const carrying = (root: ParentNode) => ([...root.querySelectorAll('[data-sem-drag]')] as HTMLElement[]).find((d) => d.getAttribute('data-sem-drag') === s.carried)
    const el = (zone?.isConnected ? carrying(zone) : undefined) ?? carrying(this.renderer.host)
    el?.focus?.({ preventScroll: false })
  }

  // --- pointer --------------------------------------------------------------------------------------

  /**
   * A press on a draggable (data-sem-gesture="drag"): a mouse or pen lifts it once it has moved a
   * few pixels, a touch after a still, long press (moving first scrolls). Then the element follows
   * the pointer, the zone and position under it go to the graph as they change, the page scrolls
   * near its edges, and releasing drops it. Escape (or a cancelled pointer) puts it back.
   */
  pointer(e: PointerEvent, handle: HTMLElement) {
    if (this.session || e.isPrimary === false) return
    const doc = this.doc
    const view = doc.defaultView
    const touch = e.pointerType === 'touch'
    const start = { x: e.clientX, y: e.clientY }
    let last = start
    let lifted: Session | undefined
    let grab = { x: 0, y: 0 }
    let frame = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const original = handle.getAttribute('style')
    const follow = () => {
      // Where it is without the transient offset, so it stays under the pointer as the page scrolls.
      handle.style.setProperty('translate', '')
      const r = handle.getBoundingClientRect?.()
      handle.style.setProperty('translate', `${last.x - grab.x - (r?.left ?? 0)}px ${last.y - grab.y - (r?.top ?? 0)}px`)
    }
    const liftNow = (alt: boolean) => {
      const r = handle.getBoundingClientRect?.()
      grab = { x: start.x - (r?.left ?? 0), y: start.y - (r?.top ?? 0) }
      lifted = this.lift(handle, 'pointer', alt, () => {
        if (original === null) handle.removeAttribute('style')
        else handle.setAttribute('style', original)
        doc.documentElement.removeAttribute('data-sem-dragging')
        if (frame) view?.cancelAnimationFrame?.(frame)
      })
      if (!lifted) return stop()
      handle.style.setProperty('pointer-events', 'none')
      doc.documentElement.setAttribute('data-sem-dragging', '')
      doc.getSelection?.()?.removeAllRanges()
      follow()
      this.over(this.placeAt(last.x, last.y))
      if (view?.requestAnimationFrame) frame = view.requestAnimationFrame(scroll)
    }
    const scroll = () => {
      if (!lifted || this.session !== lifted) return
      if (autoscroll(doc, last.x, last.y)) follow(), this.over(this.placeAt(last.x, last.y))
      frame = view!.requestAnimationFrame(scroll)
    }
    const move = (m: PointerEvent) => {
      if (m.pointerId !== e.pointerId) return
      last = { x: m.clientX, y: m.clientY }
      if (!lifted) {
        const d = Math.hypot(last.x - start.x, last.y - start.y)
        if (touch) return void (d > TOUCH_SLOP && stop())
        if (d >= THRESHOLD) liftNow(m.altKey)
        return
      }
      if (this.session !== lifted) return
      m.preventDefault?.()
      follow()
      this.over(this.placeAt(last.x, last.y))
    }
    const up = (u: PointerEvent) => {
      if (u.pointerId !== e.pointerId) return
      stop()
      if (!lifted || this.session !== lifted) return
      last = { x: u.clientX, y: u.clientY }
      this.over(this.placeAt(last.x, last.y))
      this.release()
      // The release is not also a click on whatever is under it.
      const swallow = (c: Event) => (c.stopPropagation(), c.preventDefault())
      doc.addEventListener('click', swallow, { capture: true, once: true })
      setTimeout(() => doc.removeEventListener('click', swallow, { capture: true }), 0)
    }
    const cancelled = () => {
      stop()
      if (lifted && this.session === lifted) this.cancel()
    }
    // A touch that lifted holds the page still (no scrolling while it is carried).
    const still = (t: Event) => lifted && t.cancelable && t.preventDefault()
    const stop = () => {
      if (timer) clearTimeout(timer)
      doc.removeEventListener('pointermove', move)
      doc.removeEventListener('pointerup', up)
      doc.removeEventListener('pointercancel', cancelled)
      doc.removeEventListener('touchmove', still)
    }
    doc.addEventListener('pointermove', move)
    doc.addEventListener('pointerup', up)
    doc.addEventListener('pointercancel', cancelled)
    if (touch) {
      doc.addEventListener('touchmove', still, { passive: false })
      timer = setTimeout(() => !lifted && liftNow(false), LONG_PRESS)
    }
  }

  // --- keyboard --------------------------------------------------------------------------------------

  /**
   * Keys of the keyboard drag (the beh:key of dnd:Start, dnd:Over, dnd:Drop, dnd:Cancel): on a
   * focused draggable, Space lifts it at its place; Up and Down move it between positions (in a
   * zone without positions, to the zone above or below), Left and Right between zones (mirrored
   * right to left); Space or Enter drops; Escape and Tab put it
   * back. During a pointer drag, Escape cancels it. Returns whether the key was used.
   */
  key(k: KeyboardEvent, apple: boolean): boolean {
    const combo = comboOf(k, apple)
    const s = this.session
    if (!s) {
      const el = k.target as HTMLElement | null
      if (!el?.matches?.('[data-sem-gesture=drag]') || !this.renderer.host.contains(el) || !this.keys(DND.Start).includes(combo)) return false
      const zones = this.zones()
      const lifted = this.lift(el, 'keyboard', false, () => {})
      if (!lifted) return false
      const zone = lifted.origin ? zones.indexOf(lifted.origin.zone) : -1
      if (zone >= 0) {
        const all = ([...lifted.origin!.zone.querySelectorAll('[data-sem-drag]')] as HTMLElement[]).filter((d) => this.zoneOf(d.parentElement) === lifted.origin!.zone)
        lifted.slot = { zone, index: Math.max(0, all.indexOf(el)) }
        this.over(lifted.origin)
      }
      return true
    }
    if (combo === 'tab') return this.cancel(), false
    if (this.keys(DND.Cancel).includes(combo)) return this.cancel(), true
    if (s.mode === 'pointer') return false
    if (this.keys(DND.Drop).includes(combo)) return this.release(), true
    if (!this.keys(DND.Over).includes(combo)) return false
    const zones = this.zones()
    const slot = s.slot ?? { zone: 0, index: 0 }
    const rtl = this.doc.documentElement.dir === 'rtl'
    const step = (n: number) => Math.max(0, Math.min(zones.length - 1, slot.zone + n))
    let { zone, index } = slot
    const vertical = combo === 'arrowup' || combo === 'arrowdown'
    // In a zone without positions (a calendar's cell), Up and Down go to the zone above or below.
    if (vertical && zones[zone] && !zones[zone]!.hasAttribute('data-sem-drop-ordered')) zone = this.verticalNeighbour(zones, zone, combo === 'arrowup' ? -1 : 1)
    else if (combo === 'arrowup') index--
    else if (combo === 'arrowdown') index++
    else zone = step((combo === 'arrowright') !== rtl ? 1 : -1)
    const z = zones[zone]
    if (!z) return true
    const n = z.hasAttribute('data-sem-drop-ordered') ? this.itemsOf(z).length : 0
    s.slot = { zone, index: Math.max(0, Math.min(n, index)) }
    const place = this.placeIn(z, s.slot.index)
    this.over(place)
    // Where it would go is in view (the pointer does this by autoscroll).
    const mark = this.itemsOf(z)[s.slot.index] ?? this.itemsOf(z)[s.slot.index - 1] ?? z
    mark.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
    return true
  }

  /**
   * The nearest zone straight above (-1) or below (1) the given one: the closest whose box is on
   * that side and overlaps it horizontally. The same zone when there is none (or no layout).
   */
  private verticalNeighbour(zones: HTMLElement[], at: number, dir: -1 | 1): number {
    const from = zones[at]!.getBoundingClientRect()
    let best = at
    let distance = Infinity
    zones.forEach((z, i) => {
      if (i === at) return
      const r = z.getBoundingClientRect()
      const overlaps = Math.min(r.right, from.right) - Math.max(r.left, from.left) > 1
      const gap = dir > 0 ? r.top - from.top : from.top - r.top
      if (overlaps && gap > 1 && gap < distance) {
        best = i
        distance = gap
      }
    })
    return best
  }

  /** The zones shown, in document order. */
  private zones(): HTMLElement[] {
    return ([...this.renderer.host.querySelectorAll('[data-sem-drop-zone]')] as HTMLElement[]).filter((z) => !z.closest('[hidden], [inert]'))
  }

  dispose() {
    this.live?.remove()
  }
}

/** One step of scrolling near an edge: the scrolling container under the point, else the page. Whether it scrolled. */
function autoscroll(doc: Document, x: number, y: number): boolean {
  const view = doc.defaultView
  if (!view) return false
  const speed = (into: number) => (into < EDGE ? Math.ceil(((EDGE - into) / EDGE) * MAX_STEP) : 0)
  for (let el = doc.elementFromPoint?.(x, y) ?? null; el && el !== doc.documentElement; el = el.parentElement) {
    if (el.scrollHeight <= el.clientHeight + 1 && el.scrollWidth <= el.clientWidth + 1) continue
    const style = view.getComputedStyle?.(el)
    if (!style || !/(auto|scroll)/.test(style.overflowY + style.overflowX)) continue
    const r = el.getBoundingClientRect()
    const dy = speed(r.bottom - y) - speed(y - r.top)
    const dx = speed(r.right - x) - speed(x - r.left)
    const before = [el.scrollTop, el.scrollLeft]
    if (dy || dx) el.scrollBy?.(dx, dy)
    if (el.scrollTop !== before[0] || el.scrollLeft !== before[1]) return true
  }
  const dy = speed(view.innerHeight - y) - speed(y)
  if (!dy) return false
  const before = view.scrollY
  view.scrollBy?.(0, dy)
  return view.scrollY !== before
}

