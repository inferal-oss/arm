// The host side of walkthroughs (ontology/components/walkthrough.ttl): what only the page knows,
// where things are. The graph says which step is being read (c:current), what each phrase
// mentions (data-sem-mentions on a c:Mention) and which statement of the source to show
// (data-sem-highlighted lines); this effect
//   - reports the step that crosses the reading line while the page scrolls, as an unrecorded
//     c:StepInView event (a "sem-step" DOM event on the rendered step: the bus records it),
//   - brings the first highlighted line of a source view into view inside its scroll box,
//   - says how much a sticky app bar covers (--sem-sticky-top), so the diagram sticks below it.
// The arrows from mentions to nodes are connectors (c:WalkthroughArrows), drawn by the connector
// effect (src/web/connectors.ts) like any other. Motion follows mot:preference (data-sem-motion;
// System and no preference follow the system's reduced-motion setting, src/layers/motion.ts).
import { motionCalmedAt } from '../layers/motion.ts'

/** Where the reader's eye is: the step whose top has passed this fraction of the viewport is being read. */
const READING_LINE = 0.4

export interface WalkthroughHost {
  /** Re-measure now (e.g. after the renderer patched the page). */
  update(): void
  dispose(): void
}

export function attachWalkthroughs(host: HTMLElement): WalkthroughHost {
  const doc = host.ownerDocument
  const win = doc.defaultView!
  let frame = 0
  let scrolled = false
  const reported = new WeakMap<Element, Element>()

  const reduced = motionCalmedAt
  const shown = (el: Element) => !el.closest('[hidden]') && el.getClientRects().length > 0

  const schedule = () => {
    if (!frame) frame = win.requestAnimationFrame(run)
  }
  const onScroll = () => {
    scrolled = true
    schedule()
  }

  /** How much a sticky bar covers at the top of the page: what stays in view sticks below it (--sem-sticky-top). */
  let stickyTop = -1
  function measureStickyTop() {
    const bar = doc.querySelector('[data-sem-c=app-bar]')
    const style = bar && win.getComputedStyle(bar)
    const top = bar && (style!.position === 'sticky' || style!.position === 'fixed') ? Math.ceil(bar.getBoundingClientRect().height) + 12 : 16
    if (top !== stickyTop) doc.documentElement.style.setProperty('--sem-sticky-top', `${(stickyTop = top)}px`)
  }

  function run() {
    frame = 0
    measureStickyTop()
    for (const root of host.querySelectorAll<HTMLElement>('[data-sem-c=walkthrough]')) {
      if (!shown(root)) continue
      if (scrolled) report(root)
    }
    scrolled = false
  }

  /** The step crossing the reading line becomes the current one (once per change). */
  function report(root: HTMLElement) {
    const steps = [...root.querySelectorAll<HTMLElement>(':scope > .sem-walk-body > .sem-walk-steps > .sem-walk-step')]
    if (!steps.length) return
    const rect = root.getBoundingClientRect()
    if (rect.bottom < 0 || rect.top > win.innerHeight) return
    // Under a diagram that stays at the top (a phone), the reading line is as far into what is left.
    const cover = coverOf(root)
    const line = cover ? cover + (win.innerHeight - cover) * READING_LINE * 0.75 : win.innerHeight * READING_LINE
    let reading = steps[0]!
    for (const s of steps) if (s.getBoundingClientRect().top <= line) reading = s
    // At the end of the page the last steps cannot reach the line: the lowest one fully in view is read.
    const atEnd = win.scrollY + win.innerHeight >= doc.documentElement.scrollHeight - 2
    if (atEnd) for (const s of steps) if (s.getBoundingClientRect().bottom <= win.innerHeight) reading = s
    if (reading.getAttribute('aria-current') === 'step' || reported.get(root) === reading) return
    reported.set(root, reading)
    reading.dispatchEvent(new win.CustomEvent('sem-step', { bubbles: true }))
  }

  /**
   * Where a diagram that stays at the top of the screen ends: the aside shown before the steps
   * (as on a phone, the theme's order) and sticky covers the steps that scroll under it. 0 otherwise.
   */
  function coverOf(root: HTMLElement): number {
    const aside = root.querySelector<HTMLElement>(':scope > .sem-walk-body > .sem-walk-aside')
    const steps = root.querySelector<HTMLElement>(':scope > .sem-walk-body > .sem-walk-steps')
    if (!aside || !steps) return 0
    const a = win.getComputedStyle(aside)
    if (a.position !== 'sticky' || (Number(a.order) || 0) >= (Number(win.getComputedStyle(steps).order) || 0)) return 0
    return Math.max(0, aside.getBoundingClientRect().bottom)
  }

  /** Bring a source view's first highlighted line into view within its own scroll box. */
  function reveal(line: Element) {
    const pre = line.closest<HTMLElement>('.sem-source')
    if (!pre || !shown(pre)) return
    const first = pre.querySelector('.sem-line[data-sem-highlighted]')
    if (first !== line) return
    const box = pre.getBoundingClientRect()
    const at = first.getBoundingClientRect()
    const smooth = !reduced(pre)
    pre.scrollTo?.({ top: pre.scrollTop + at.top - box.top - pre.clientHeight * 0.2, behavior: smooth ? 'smooth' : 'auto' })
    // Stacked under the steps (a narrow page), the box itself may be out of view.
    if (box.bottom < 0 || box.top > win.innerHeight) pre.scrollIntoView?.({ block: 'nearest', behavior: smooth ? 'smooth' : 'auto' })
  }

  const observer = new win.MutationObserver((records) => {
    for (const r of records) {
      if (r.type === 'attributes' && r.attributeName === 'data-sem-highlighted' && (r.target as Element).hasAttribute('data-sem-highlighted')) reveal(r.target as Element)
    }
    schedule()
  })
  observer.observe(host, { subtree: true, childList: true, attributes: true, attributeFilter: ['aria-current', 'data-sem-highlighted', 'hidden'] })
  win.addEventListener('scroll', onScroll, { passive: true, capture: true })
  win.addEventListener('resize', schedule, { passive: true })
  schedule()
  return {
    update: schedule,
    dispose() {
      observer.disconnect()
      win.removeEventListener('scroll', onScroll, { capture: true })
      win.removeEventListener('resize', schedule)
      if (frame) win.cancelAnimationFrame(frame)
    },
  }
}
