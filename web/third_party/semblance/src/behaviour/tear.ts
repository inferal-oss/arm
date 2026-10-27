// Tearing a tab off into a window, and dragging the window back (ontology/components/window.ttl).
// Like the other gestures, these only change the view while the pointer is down - a ghost of the
// window to be follows it, the tab list it would return to lights up - and say what happened as one
// RDF event on release: a beh:TearOff with where the window goes, or a beh:Move onto the list.
// Whether that detaches or docks anything is the reactions' business.
import { C } from '../ns.ts'
import type { DomRenderer } from '../render/dom.ts'
import type { GraphStore } from '../store/types.ts'

/** A touch tears after a still, long press; moving first scrolls (as the drag and drop layer does). */
const TOUCH_SLOP = 10
const LONG_PRESS = 350
const TEAR_DEFAULT = 24

/** How far a point is outside an element's box (0 inside it). */
function outside(el: Element, x: number, y: number): number {
  const r = el.getBoundingClientRect()
  return Math.max(r.left - x, x - r.right, r.top - y, y - r.bottom, 0)
}

/** The release is not also a click on whatever is under it. */
function swallowClick(doc: Document) {
  const swallow = (c: Event) => (c.stopPropagation(), c.preventDefault())
  doc.addEventListener('click', swallow, { capture: true, once: true })
  setTimeout(() => doc.removeEventListener('click', swallow, { capture: true }), 0)
}

/**
 * A press on a detachable tab (data-sem-gesture="tear"). Nothing happens until the pointer is
 * the list's data-sem-tear-distance outside its tab list (a mouse or pen at once, a touch after a
 * long press): until then the press is the tab's, and letting go is a click. Past it, a ghost of the
 * window follows the pointer (in the top layer, so no clipping frame hides it) and says what letting
 * go does; back within the distance it says the tab stays. Let go out there, the tab dispatches one
 * sem-tear-off event with the ghost's top-left (beh:x, beh:y); let go back by the list, or Escape,
 * and nothing is recorded.
 */
export function tearGesture(e: PointerEvent, tab: HTMLElement): void {
  if (e.button !== 0 || e.isPrimary === false) return
  const list = tab.closest('[role=tablist]') as HTMLElement | null
  if (!list) return
  const doc = tab.ownerDocument
  const view = doc.defaultView
  const distance = Number(list.getAttribute('data-sem-tear-distance')) || TEAR_DEFAULT
  const touch = e.pointerType === 'touch'
  const start = { x: e.clientX, y: e.clientY }
  // Where in its title bar the window is held: about where the tab was.
  const r = tab.getBoundingClientRect()
  const grab = { x: Math.max(16, Math.min(start.x - r.left, 160)), y: Math.max(10, Math.min(start.y - r.top, 18)) }
  let armed = !touch
  let ghost: HTMLElement | undefined
  let back = false
  let at = { x: 0, y: 0 }
  let timer: ReturnType<typeof setTimeout> | undefined

  const name = (tab.querySelector('.sem-tab-label')?.textContent ?? tab.textContent ?? '').trim()
  const cue = () => list.getAttribute(back ? 'data-sem-tear-back-cue' : 'data-sem-tear-cue') ?? ''
  // The ghost is drawn as a window (the frame's hooks: data-sem-c, its bar and body), so each theme
  // shows the window it would become in its own look; data-sem-ghost makes it an outline of one.
  const show = () => {
    const g = doc.createElement('section')
    g.setAttribute('data-sem-c', 'window')
    g.setAttribute('data-sem-ghost', '')
    g.setAttribute('data-sem-window-state', 'normal')
    g.setAttribute('aria-hidden', 'true')
    const bar = doc.createElement('header')
    bar.className = 'sem-window-bar'
    const title = doc.createElement('strong')
    title.textContent = name
    bar.appendChild(title)
    const line = doc.createElement('div')
    line.className = 'sem-window-body sem-tear-cue'
    g.append(bar, line)
    ghost = g
    attach()
    tab.setAttribute('data-sem-tearing', '')
    doc.documentElement.setAttribute('data-sem-dragging', '')
    doc.getSelection?.()?.removeAllRanges()
  }
  // In the tabs' own subtree (it takes their theme), raised into the top layer where it can be.
  const attach = () => {
    const g = ghost!
    ;(tab.closest('[data-sem-c=tabs]') ?? list.parentElement ?? list).appendChild(g)
    if (typeof g.showPopover === 'function') {
      g.setAttribute('popover', 'manual')
      g.showPopover()
    }
  }
  const draw = (x: number, y: number) => {
    const g = ghost!
    if (!g.isConnected) attach()
    const w = view?.innerWidth ?? 0
    const h = view?.innerHeight ?? 0
    // On screen: all of its width where there is room, its title bar always.
    const width = Math.max(48, Math.min(g.offsetWidth || 0, w))
    at = { x: Math.round(Math.max(0, Math.min(x - grab.x, w - width))), y: Math.round(Math.max(0, Math.min(y - grab.y, h - 32))) }
    g.style.setProperty('left', `${at.x}px`)
    g.style.setProperty('top', `${at.y}px`)
    g.setAttribute('data-sem-tear', back ? 'back' : 'out')
    const line = g.querySelector('.sem-tear-cue')!
    if (line.textContent !== cue()) line.textContent = cue()
    if (back) list.setAttribute('data-sem-gathering', '')
    else list.removeAttribute('data-sem-gathering')
  }

  const move = (m: PointerEvent) => {
    if (m.pointerId !== e.pointerId) return
    if (!armed) return void (Math.hypot(m.clientX - start.x, m.clientY - start.y) > TOUCH_SLOP && stop())
    const far = outside(list, m.clientX, m.clientY)
    if (!ghost) {
      if (far < distance) return
      show()
    }
    m.preventDefault?.()
    back = far < distance
    draw(m.clientX, m.clientY)
  }
  const up = (u: PointerEvent) => {
    if (u.pointerId !== e.pointerId) return
    const torn = ghost !== undefined
    if (torn) {
      back = outside(list, u.clientX, u.clientY) < distance
      draw(u.clientX, u.clientY)
    }
    const where = at
    const keep = back
    stop()
    if (!torn) return
    swallowClick(doc)
    if (keep) return
    const Custom = view?.CustomEvent ?? CustomEvent
    tab.dispatchEvent(new Custom('sem-tear-off', { bubbles: true, detail: { x: where.x, y: where.y } }))
  }
  const escape = (k: KeyboardEvent) => {
    if (k.key !== 'Escape' || !ghost) return
    k.preventDefault()
    k.stopImmediatePropagation()
    stop()
  }
  // A touch that tore holds the page still; a long press does not open the context menu.
  const still = (t: Event) => armed && touch && t.cancelable && t.preventDefault()
  const menu = (c: Event) => touch && c.preventDefault()
  const stop = () => {
    if (timer) clearTimeout(timer)
    doc.removeEventListener('pointermove', move)
    doc.removeEventListener('pointerup', up)
    doc.removeEventListener('pointercancel', stop)
    doc.removeEventListener('keydown', escape, true)
    doc.removeEventListener('touchmove', still)
    doc.removeEventListener('contextmenu', menu, true)
    tab.removeAttribute('data-sem-tear-armed')
    if (!ghost) return
    if (typeof ghost.hidePopover === 'function' && ghost.matches?.(':popover-open')) ghost.hidePopover()
    ghost.remove()
    ghost = undefined
    tab.removeAttribute('data-sem-tearing')
    list.removeAttribute('data-sem-gathering')
    doc.documentElement.removeAttribute('data-sem-dragging')
  }
  doc.addEventListener('pointermove', move)
  doc.addEventListener('pointerup', up)
  doc.addEventListener('pointercancel', stop)
  doc.addEventListener('keydown', escape, true)
  if (touch) {
    doc.addEventListener('touchmove', still, { passive: false })
    doc.addEventListener('contextmenu', menu, true)
    timer = setTimeout(() => {
      armed = true
      tab.setAttribute('data-sem-tear-armed', '')
    }, LONG_PRESS)
  }
}

/**
 * While a frame is moved by its title bar: the tab lists it may return to (those that c:gathers
 * it), the one under the pointer, and the marks that show it would (the list lights up, the frame
 * says "release to return it"). Nothing for a frame no list gathers.
 */
export function returnPlaces(store: GraphStore, graph: Parameters<GraphStore['match']>[3], renderer: DomRenderer, box: HTMLElement, frame: string) {
  const f = store.factory
  const lists = store.match(null, f.namedNode(C.gathers), f.namedNode(frame), graph).map((q) => q.subject.value)
  if (!lists.length) return undefined
  let marked: HTMLElement | undefined
  return {
    /** The list under a point, if any (its key: the RDF/HTML node beh:onto names). */
    at(x: number, y: number): { el: HTMLElement; key: string } | undefined {
      for (const key of lists) {
        const el = renderer.nodeOf(key) as HTMLElement | undefined
        if (el?.isConnected && !el.closest('[hidden], [inert]') && outside(el, x, y) === 0) return { el, key }
      }
      return undefined
    },
    mark(el: HTMLElement | undefined) {
      if (el === marked) return
      marked?.removeAttribute('data-sem-gathering')
      marked = el
      if (el) {
        el.setAttribute('data-sem-gathering', '')
        box.setAttribute('data-sem-returning', '')
        box.setAttribute('data-sem-return-cue', el.getAttribute('data-sem-return-cue') ?? '')
      } else {
        box.removeAttribute('data-sem-returning')
        box.removeAttribute('data-sem-return-cue')
      }
    },
  }
}
