// The host side of snap points (data-sem-snap: a gallery's carousel track, a lightbox's frames). The
// graph says which item is current (data-sem-current on it); this effect does what only the page
// can:
//   - brings the current item into view when the graph moved it (an arrow, a key, a thumbnail):
//     smoothly with full motion, at once with reduced or no motion (mot:preference, or the system's
//     setting), and at once when the item is new (a lightbox re-keys its frames around the current
//     one, so the one it shows must not slide in from a neighbour's place);
//   - reports where a scroll or a swipe came to rest: a "sem-snap" DOM event on the item nearest the
//     middle of the track, which the bus records as an unrecorded c:Snapped, so it becomes current.
// It never writes the graph itself, and it does nothing while the page has no snap track.

export interface SnapHost {
  /** Bring every track's current item into view now (it also runs by itself when the DOM changes). */
  update(): void
  dispose(): void
}

/** How long a track must be still before its position counts as where it came to rest. */
const SETTLE_MS = 120

export function attachSnaps(host: HTMLElement): SnapHost {
  const doc = host.ownerDocument
  const win = doc.defaultView!
  /** Items that were there at the last update: one that was not is jumped to, not slid to. */
  let seen = new WeakSet<Element>()
  /** Where each track was left: moved since by a re-render (not by the reader), it jumps. */
  const left = new WeakMap<Element, number>()
  const settling = new WeakMap<Element, ReturnType<typeof setTimeout>>()
  let frame = 0

  const reduced = (el: Element) => {
    const pref = el.closest('[data-sem-motion]')?.getAttribute('data-sem-motion')
    return pref === 'reduced' || pref === 'none' || !!win.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  }
  /** How far the track must scroll for `item` to be in its middle (negative: towards the left edge). */
  const offset = (track: Element, item: Element) => {
    const t = track.getBoundingClientRect()
    const r = item.getBoundingClientRect()
    return r.left + r.width / 2 - (t.left + t.width / 2)
  }
  const items = (track: Element) => [...track.children].filter((x) => x.getClientRects().length > 0)

  function update() {
    frame = 0
    const now = new WeakSet<Element>()
    for (const track of host.querySelectorAll<HTMLElement>('[data-sem-snap]')) {
      for (const item of track.children) now.add(item)
      const current = track.querySelector(':scope > [data-sem-current]')
      // A zoomed image's box is the person's to scroll.
      if (!current || track.getClientRects().length === 0 || track.closest('[data-zoomed]')) continue
      const by = offset(track, current)
      const jump = !seen.has(current) || left.get(track) !== track.scrollLeft
      if (Math.abs(by) >= 1) track.scrollBy({ left: by, behavior: jump || reduced(track) ? 'instant' : 'smooth' })
      left.set(track, jump || reduced(track) ? track.scrollLeft : track.scrollLeft + by)
    }
    seen = now
  }
  const schedule = () => {
    if (!frame) frame = win.requestAnimationFrame(update)
  }

  /** The track came to rest: the item nearest its middle is where the reader is. */
  function settle(track: HTMLElement) {
    settling.delete(track)
    if (track.closest('[data-zoomed]')) return
    left.set(track, track.scrollLeft)
    let nearest: Element | undefined
    let best = Infinity
    for (const item of items(track)) {
      const d = Math.abs(offset(track, item))
      if (d < best) (best = d), (nearest = item)
    }
    if (!nearest || nearest.hasAttribute('data-sem-current')) return
    nearest.dispatchEvent(new win.CustomEvent('sem-snap', { bubbles: true }))
  }
  // A browser that says when a scroll has ended (smooth or snapping, however long it takes) is
  // asked; elsewhere a track that has been still for a moment has come to rest.
  const ends = 'onscrollend' in win
  const onScroll = (e: Event) => {
    const track = e.target as HTMLElement
    if (!track.hasAttribute?.('data-sem-snap')) return
    clearTimeout(settling.get(track))
    if (e.type === 'scrollend') settle(track)
    else if (!ends) settling.set(track, setTimeout(() => settle(track), SETTLE_MS))
  }
  // Scroll events do not bubble: listen in the capture phase.
  host.addEventListener('scroll', onScroll, true)
  host.addEventListener('scrollend', onScroll, true)
  const observer = new win.MutationObserver(schedule)
  observer.observe(host, { subtree: true, childList: true, attributes: true, attributeFilter: ['data-sem-current'] })
  win.addEventListener('resize', schedule)
  schedule()

  return {
    update,
    dispose() {
      host.removeEventListener('scroll', onScroll, true)
      host.removeEventListener('scrollend', onScroll, true)
      observer.disconnect()
      win.removeEventListener('resize', schedule)
      if (frame) win.cancelAnimationFrame(frame)
    },
  }
}
