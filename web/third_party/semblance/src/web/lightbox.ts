// The host side of lightboxes (ontology/components/gallery.ttl): the movement only the page can
// make. The graph says which image is shown (the frame marked data-sem-current), where it is in its
// gallery (data-sem-origin: that element's id) and whether it is zoomed (data-zoomed on the stage);
// this effect
//   - grows the viewer's image from where it is in the gallery when the lightbox opens, and returns
//     it there when it closes (a copy flies back, as the lightbox is already gone);
//   - slides the next image in from the side it comes from (but not after a swipe, which already
//     brought it) - mirrored right to left;
//   - zooms from the image's place to its own size, keeping a double-clicked point under the
//     pointer, and pans a zoomed image by dragging it.
// Every movement is timed by the motion tokens on the element (--sem-duration-move for the long
// ones, --sem-duration-slow for a slide): reduced motion turns the long movements into a short fade,
// none turns them off. It never writes the graph.

export interface LightboxHost {
  dispose(): void
}

interface Shown {
  img: HTMLImageElement
  rect: DOMRect
  src: string
  origin: string
  zoomed: boolean
}

const ms = (v: string) => {
  const s = v.trim()
  return s.endsWith('ms') ? parseFloat(s) : s.endsWith('s') ? parseFloat(s) * 1000 : 0
}

export function attachLightboxes(host: HTMLElement): LightboxHost {
  const doc = host.ownerDocument
  const win = doc.defaultView!
  /** What each open viewer (its stage) last showed, measured. */
  const shown = new Map<Element, Shown>()
  /** Stages whose current image just came by a swipe: nothing slides in. */
  const swiped = new WeakSet<Element>()
  /** Where a double-click asked to zoom: a point of the image, as fractions of its size, and on the screen. */
  let aim: { img: HTMLImageElement; fx: number; fy: number; x: number; y: number } | undefined
  let frame = 0

  const timing = (el: Element) => {
    const css = win.getComputedStyle(el)
    return {
      move: ms(css.getPropertyValue('--sem-duration-move')),
      slow: ms(css.getPropertyValue('--sem-duration-slow')),
      fade: ms(css.getPropertyValue('--sem-duration')),
      ease: css.getPropertyValue('--sem-ease-move').trim() || 'ease',
    }
  }
  const visible = (r: DOMRect) => r.width > 0 && r.bottom > 0 && r.right > 0 && r.top < win.innerHeight && r.left < win.innerWidth
  /** The transform that puts a box of `to` where `from` is. */
  const from = (a: DOMRect, b: DOMRect) =>
    `translate(${a.left + a.width / 2 - (b.left + b.width / 2)}px, ${a.top + a.height / 2 - (b.top + b.height / 2)}px) scale(${a.width / b.width}, ${a.height / b.height})`
  const place = (img: Element) => Number(/-(\d+)-img$/.exec(img.getAttribute('data-sem-origin') ?? '')?.[1] ?? 0)
  const rtl = (el: Element) => win.getComputedStyle(el).direction === 'rtl'

  /**
   * A copy of an image flies, above everything, from one place to another: the viewer's image
   * itself sits in a box that scrolls, which would clip it on the way.
   */
  function fly(src: string, start: DOMRect, end: DOMRect, t: ReturnType<typeof timing>, fade: [number, number]): Animation {
    const copy = doc.createElement('img')
    copy.src = src
    copy.alt = ''
    copy.setAttribute('aria-hidden', 'true')
    Object.assign(copy.style, {
      position: 'fixed', left: `${end.left}px`, top: `${end.top}px`, width: `${end.width}px`, height: `${end.height}px`,
      margin: '0', zIndex: '61', pointerEvents: 'none', objectFit: 'cover',
    })
    doc.body.append(copy)
    const a = copy.animate([{ transform: from(start, end), opacity: fade[0] }, { transform: 'none', opacity: fade[1] }], { duration: t.move, easing: t.ease, fill: 'forwards' })
    a.onfinish = a.oncancel = () => copy.remove()
    return a
  }

  /** The viewer opened: its image grows from where it is in the gallery (it waits, hidden, meanwhile). */
  function grow(img: HTMLImageElement) {
    const origin = doc.getElementById(img.getAttribute('data-sem-origin') ?? '')
    const t = timing(img)
    const start = origin?.getBoundingClientRect()
    const end = img.getBoundingClientRect()
    if (t.move && start && visible(start) && end.width) {
      img.animate([{ opacity: 0 }, { opacity: 0 }], { duration: t.move })
      fly(img.currentSrc || img.src, start, end, t, [1, 1])
    } else if (t.fade) img.animate([{ opacity: 0 }, { opacity: 1 }], { duration: t.fade })
  }

  /** The viewer closed: a copy of its image flies back to where it is in the gallery. */
  function giveBack(last: Shown) {
    const origin = doc.getElementById(last.origin)
    const end = origin?.getBoundingClientRect()
    const t = timing(origin ?? host)
    if (!t.move || !end || !visible(end) || !last.rect.width) return
    fly(last.src, last.rect, end, t, [1, 0.6])
  }

  /** Another image became current: it slides in from the side it comes from. */
  function slide(stage: Element, img: HTMLImageElement, before: Shown) {
    const t = timing(img)
    const a = place(before.img)
    const b = place(img)
    if (!a || !b || a === b) return
    // Around the end of a looping viewer, the step is the short way.
    const step = Math.abs(b - a) > 1 ? Math.sign(a - b) : Math.sign(b - a)
    const dx = step * (rtl(stage) ? -1 : 1) * Math.min(64, stage.getBoundingClientRect().width * 0.08)
    if (t.move) img.animate([{ transform: `translateX(${dx}px)`, opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: t.slow, easing: t.ease })
    else if (t.fade) img.animate([{ opacity: 0 }, { opacity: 1 }], { duration: t.fade })
  }

  /** The zoom changed: the image grows from (or shrinks to) where it was, around the aimed point. */
  function zoom(stage: Element, img: HTMLImageElement, before: Shown, zoomed: boolean) {
    const box = img.closest('[data-sem-snap]') as HTMLElement | null
    if (zoomed && box && aim?.img === img) {
      // Keep the double-clicked point of the image under the pointer.
      const r = img.getBoundingClientRect()
      box.scrollLeft += r.left + aim.fx * r.width - aim.x
      box.scrollTop += r.top + aim.fy * r.height - aim.y
    } else if (zoomed && box) {
      box.scrollLeft = (box.scrollWidth - box.clientWidth) / 2
      box.scrollTop = (box.scrollHeight - box.clientHeight) / 2
    }
    aim = undefined
    const t = timing(img)
    if (t.move) img.animate([{ transform: from(before.rect, img.getBoundingClientRect()) }, { transform: 'none' }], { duration: Math.min(t.move, 420), easing: t.ease })
    void stage
  }

  function update() {
    frame = 0
    const open = new Set<Element>()
    for (const img of host.querySelectorAll<HTMLImageElement>('[data-sem-current] > img[data-sem-origin]')) {
      const stage = img.closest('.sem-stage') ?? img.parentElement!
      if (img.closest('[hidden]')) continue
      open.add(stage)
      const zoomed = stage.hasAttribute('data-zoomed')
      const before = shown.get(stage)
      if (!before) grow(img)
      else if (before.zoomed !== zoomed) zoom(stage, img, before, zoomed)
      else if (before.img !== img && !swiped.has(stage)) slide(stage, img, before)
      swiped.delete(stage)
      shown.set(stage, { img, rect: img.getBoundingClientRect(), src: img.currentSrc || img.src, origin: img.getAttribute('data-sem-origin') ?? '', zoomed })
      // Measured mid-movement, the place is where it is going: measure again once it is there.
      for (const a of img.getAnimations?.() ?? []) a.finished.then(remeasure, () => {})
    }
    for (const [stage, last] of shown) {
      if (open.has(stage)) continue
      shown.delete(stage)
      giveBack(last)
    }
  }
  const schedule = () => {
    if (!frame) frame = win.requestAnimationFrame(update)
  }
  /** Keep the last place of each shown image current (a closing viewer flies back from there). */
  const remeasure = () => {
    for (const [stage, s] of shown) if (s.img.isConnected) shown.set(stage, { ...s, rect: s.img.getBoundingClientRect() })
  }

  const onSnap = (e: Event) => {
    const stage = (e.target as Element).closest?.('.sem-stage')
    if (stage) swiped.add(stage)
  }
  const onDoubleClick = (e: MouseEvent) => {
    const img = e.target as HTMLImageElement
    if (!img.matches?.('[data-sem-current] > img[data-sem-origin]') || img.closest('[data-zoomed]')) return
    const r = img.getBoundingClientRect()
    aim = { img, fx: (e.clientX - r.left) / r.width, fy: (e.clientY - r.top) / r.height, x: e.clientX, y: e.clientY }
  }
  // Pan: dragging a zoomed image scrolls its box (touch scrolls it natively).
  let pan: { box: HTMLElement; x: number; y: number; id: number } | undefined
  const onDown = (e: PointerEvent) => {
    const img = e.target as Element
    if (e.pointerType === 'touch' || e.button !== 0 || !img.matches?.('[data-zoomed] [data-sem-current] > img')) return
    const box = img.closest('[data-sem-snap]') as HTMLElement | null
    if (!box) return
    e.preventDefault()
    pan = { box, x: e.clientX, y: e.clientY, id: e.pointerId }
  }
  const onMove = (e: PointerEvent) => {
    if (!pan || e.pointerId !== pan.id) return
    pan.box.scrollLeft -= e.clientX - pan.x
    pan.box.scrollTop -= e.clientY - pan.y
    pan.x = e.clientX
    pan.y = e.clientY
  }
  const onUp = () => void (pan = undefined)

  const observer = new win.MutationObserver(schedule)
  observer.observe(host, { subtree: true, childList: true, attributes: true, attributeFilter: ['data-sem-current', 'data-zoomed', 'hidden'] })
  host.addEventListener('sem-snap', onSnap, true)
  host.addEventListener('dblclick', onDoubleClick, true)
  host.addEventListener('pointerdown', onDown, true)
  doc.addEventListener('pointermove', onMove)
  doc.addEventListener('pointerup', onUp)
  doc.addEventListener('pointercancel', onUp)
  win.addEventListener('scroll', remeasure, { capture: true, passive: true })
  win.addEventListener('resize', remeasure)
  schedule()

  return {
    dispose() {
      observer.disconnect()
      host.removeEventListener('sem-snap', onSnap, true)
      host.removeEventListener('dblclick', onDoubleClick, true)
      host.removeEventListener('pointerdown', onDown, true)
      doc.removeEventListener('pointermove', onMove)
      doc.removeEventListener('pointerup', onUp)
      doc.removeEventListener('pointercancel', onUp)
      win.removeEventListener('scroll', remeasure, true)
      win.removeEventListener('resize', remeasure)
      if (frame) win.cancelAnimationFrame(frame)
    },
  }
}
